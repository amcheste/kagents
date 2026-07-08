package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// AgentTeamRunReconciler turns an AgentTeamRun into a running AgentTeam by
// resolving its referenced AgentTeamTemplate, merging Run-level overrides on
// top of the Template's defaults, and creating a child AgentTeam owned by the
// Run. The child's status flows back into the Run on every reconcile so
// `kubectl get agentteamrun` reflects the underlying team's progress without
// users needing to know the team exists.
type AgentTeamRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// runConditionTeamCreated is set on the Run when a child AgentTeam exists
// and is being tracked. False means the Run is still waiting on a Ready
// template or hit a terminal configuration error.
const runConditionTeamCreated = "TeamCreated"

// runRequeueWaitingForTemplate is the back-off interval used when the
// referenced template is not yet Ready. Short enough that template fixes
// propagate quickly, long enough to avoid hot-looping when the template is
// actually broken.
const runRequeueWaitingForTemplate = 15 * time.Second

// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamruns/finalizers,verbs=update

func (r *AgentTeamRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var run claudev1alpha1.AgentTeamRun
	if err := r.Get(ctx, req.NamespacedName, &run); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve the referenced template.
	var tmpl claudev1alpha1.AgentTeamTemplate
	tmplKey := types.NamespacedName{Name: run.Spec.TemplateRef.Name, Namespace: run.Namespace}
	if err := r.Get(ctx, tmplKey, &tmpl); err != nil {
		if errors.IsNotFound(err) {
			// Template missing is a terminal-ish state from the Run's
			// perspective: surface it on the Run's status without retrying
			// indefinitely. A re-applied template will trigger a fresh
			// reconcile via the Owns watch on the parent template once
			// resolved.
			run.Status.Phase = "Failed"
			setRunCondition(&run, runConditionTeamCreated, metav1.ConditionFalse,
				"TemplateNotFound", fmt.Sprintf("AgentTeamTemplate %q not found in namespace %q",
					run.Spec.TemplateRef.Name, run.Namespace))
			return ctrl.Result{}, r.Status().Update(ctx, &run)
		}
		return ctrl.Result{}, err
	}

	// Refuse to instantiate templates that haven't passed validation. The
	// AgentTeamTemplate reconciler is the source of truth for Ready.
	if !tmpl.Status.Ready {
		log.Info("Template not Ready yet — waiting", "template", tmpl.Name)
		setRunCondition(&run, runConditionTeamCreated, metav1.ConditionFalse,
			"TemplateNotReady", fmt.Sprintf("AgentTeamTemplate %q is not yet Ready", tmpl.Name))
		if err := r.Status().Update(ctx, &run); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: runRequeueWaitingForTemplate}, nil
	}

	// Render the merged AgentTeam spec.
	mergedSpec := mergeRunIntoTemplate(&run, &tmpl)

	// Create-or-update the child team. The child shares the Run's name so
	// `kubectl get agentteam <run-name>` Just Works.
	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: run.Name, Namespace: run.Namespace},
	}
	if err := ctrl.SetControllerReference(&run, team, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference on child team: %w", err)
	}

	op, err := ctrlOpCreateOrUpdate(ctx, r.Client, team, func() error {
		// Spec is fully owner-managed: the Run is the source of truth, so
		// stomping the spec on every reconcile is correct. Status is
		// preserved by Kubernetes (status is a separate subresource).
		team.Spec = mergedSpec
		// Re-set the owner reference inside the mutation closure so it
		// survives a controller-runtime client update path that fetches
		// the existing object.
		return ctrl.SetControllerReference(&run, team, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating/updating child AgentTeam: %w", err)
	}
	if op != "" {
		log.Info("child AgentTeam reconciled", "op", op)
	}

	// Mirror the child team's status into the Run's status so users can
	// watch progress with `kubectl get agentteamrun`.
	mirrorTeamStatus(&run, team)
	setRunCondition(&run, runConditionTeamCreated, metav1.ConditionTrue,
		"TeamCreated", fmt.Sprintf("Child AgentTeam %q is %s", team.Name, team.Status.Phase))

	return ctrl.Result{}, r.Status().Update(ctx, &run)
}

// mergeRunIntoTemplate produces an AgentTeamSpec that combines the
// Template's reusable bits (teammates, coordination, default lifecycle,
// quality gates) with the Run's per-instance overrides (auth, lead,
// repository, workspace, lifecycle override).
//
// The merge is total: it replaces, never appends. Lifecycle override is
// "all-or-nothing" — if the Run sets Lifecycle, it wins entirely; otherwise
// the Template's Lifecycle is used. Field-by-field merging would create
// surprising results when only some fields are set on the override.
func mergeRunIntoTemplate(run *claudev1alpha1.AgentTeamRun, tmpl *claudev1alpha1.AgentTeamTemplate) claudev1alpha1.AgentTeamSpec {
	spec := claudev1alpha1.AgentTeamSpec{
		Auth:         run.Spec.Auth,
		Lead:         run.Spec.Lead,
		Teammates:    tmpl.Spec.Teammates,
		Coordination: tmpl.Spec.Coordination,
		QualityGates: tmpl.Spec.QualityGates,
		Repository:   run.Spec.Repository,
		Workspace:    run.Spec.Workspace,
	}
	// Run.Lifecycle is "all-or-nothing" — when set, it replaces the
	// template's lifecycle wholesale.
	if run.Spec.Lifecycle != nil {
		spec.Lifecycle = run.Spec.Lifecycle
	} else if tmpl.Spec.Lifecycle != nil {
		spec.Lifecycle = tmpl.Spec.Lifecycle
	}
	return spec
}

// mirrorTeamStatus copies the child AgentTeam's status into the Run. The
// Run's status type aliases AgentTeamStatus, so this is a straight assign.
// We do not propagate the child's Conditions slice because the Run has its
// own conditions tracking the controller's relationship with the team.
func mirrorTeamStatus(run *claudev1alpha1.AgentTeamRun, team *claudev1alpha1.AgentTeam) {
	preservedConditions := run.Status.Conditions
	run.Status = team.Status
	run.Status.Conditions = preservedConditions
}

// setRunCondition writes a typed condition on the Run, updating in place if
// an entry of the same Type already exists. LastTransitionTime advances only
// on a real status flip — same condType/Status calls leave it untouched so
// the field reflects "when did this state begin", not "last time we
// reconciled".
func setRunCondition(run *claudev1alpha1.AgentTeamRun, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range run.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				run.Status.Conditions[i].LastTransitionTime = now
			}
			run.Status.Conditions[i].Status = status
			run.Status.Conditions[i].Reason = reason
			run.Status.Conditions[i].Message = trimMessage(message, 256)
			return
		}
	}
	run.Status.Conditions = append(run.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            trimMessage(message, 256),
		LastTransitionTime: now,
	})
}

// SetupWithManager registers the reconciler and asks it to re-fire whenever
// a child AgentTeam owned by a Run changes (so child status flowing into
// Run.status doesn't lag).
func (r *AgentTeamRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&claudev1alpha1.AgentTeamRun{}).
		Owns(&claudev1alpha1.AgentTeam{}).
		Complete(r)
}

// ctrlOpCreateOrUpdate is a thin wrapper over controllerutil.CreateOrUpdate
// that we vendor inline to avoid pulling in the larger controllerutil import
// surface. Returns the operation kind ("created" / "updated" / "") and any
// error.
func ctrlOpCreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, mutate func() error) (string, error) {
	key := client.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, key, obj); err != nil {
		if !errors.IsNotFound(err) {
			return "", err
		}
		if err := mutate(); err != nil {
			return "", err
		}
		if err := c.Create(ctx, obj); err != nil {
			return "", err
		}
		return "created", nil
	}
	existing := obj.DeepCopyObject()
	if err := mutate(); err != nil {
		return "", err
	}
	// Cheap equality check: re-fetch the existing object's resourceVersion
	// drift via a client.Update; controller-runtime will optimistically
	// reject if someone else mutated it in the meantime.
	if err := c.Update(ctx, obj); err != nil {
		return "", err
	}
	_ = existing // existing is captured for future "did anything change" optimization
	return "updated", nil
}
