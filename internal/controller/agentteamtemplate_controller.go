package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// AgentTeamTemplateReconciler validates AgentTeamTemplate specs and surfaces
// the result via the template's Ready condition. It does not provision any
// runtime resources — templates are inert until an AgentTeamRun references
// them, at which point the Run controller refuses to proceed unless the
// template is Ready.
type AgentTeamTemplateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// templateConditionReady is the canonical condition Type written by this
// reconciler. AgentTeamRun controllers should look for this exact name.
const templateConditionReady = "Ready"

var validModels = map[string]struct{}{
	"opus":   {},
	"sonnet": {},
	"haiku":  {},
}

// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtemplates/finalizers,verbs=update

func (r *AgentTeamTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var tmpl claudev1alpha1.AgentTeamTemplate
	if err := r.Get(ctx, req.NamespacedName, &tmpl); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.V(1).Info("Reconciling AgentTeamTemplate")

	if err := validateTemplate(&tmpl.Spec); err != nil {
		setTemplateReady(&tmpl, false, "ValidationFailed", err.Error())
	} else {
		setTemplateReady(&tmpl, true, "Valid", "Template passed validation")
	}

	return ctrl.Result{}, r.Status().Update(ctx, &tmpl)
}

// validateTemplate enforces internal-consistency rules on the template that
// the kubebuilder-generated CRD validation alone cannot express:
//
//   - teammate names must be unique
//   - dependsOn references must point at other teammates in the same template
//   - dependsOn cannot point at the teammate itself
//   - models, when set, must be one of the allowed Anthropic model names
//
// Returns nil when the template is safe to instantiate.
func validateTemplate(spec *claudev1alpha1.AgentTeamTemplateSpec) error {
	names := make(map[string]struct{}, len(spec.Teammates))
	for _, tm := range spec.Teammates {
		if _, dup := names[tm.Name]; dup {
			return fmt.Errorf("duplicate teammate name %q", tm.Name)
		}
		names[tm.Name] = struct{}{}
	}

	for _, tm := range spec.Teammates {
		if tm.Model != "" {
			if _, ok := validModels[tm.Model]; !ok {
				return fmt.Errorf("teammate %q has invalid model %q (allowed: opus|sonnet|haiku)",
					tm.Name, tm.Model)
			}
		}
		for _, dep := range tm.DependsOn {
			if dep == tm.Name {
				return fmt.Errorf("teammate %q cannot depend on itself", tm.Name)
			}
			if _, exists := names[dep]; !exists {
				return fmt.Errorf("teammate %q depends on unknown teammate %q",
					tm.Name, dep)
			}
		}
	}
	return nil
}

// setTemplateReady writes the Ready condition and the convenience boolean.
// Idempotent: re-uses any existing condition entry rather than appending.
func setTemplateReady(tmpl *claudev1alpha1.AgentTeamTemplate, ready bool, reason, message string) {
	tmpl.Status.Ready = ready

	status := metav1.ConditionTrue
	if !ready {
		status = metav1.ConditionFalse
	}

	now := metav1.Now()
	for i, c := range tmpl.Status.Conditions {
		if c.Type == templateConditionReady {
			if c.Status != status {
				tmpl.Status.Conditions[i].LastTransitionTime = now
			}
			tmpl.Status.Conditions[i].Status = status
			tmpl.Status.Conditions[i].Reason = reason
			// Trim message to a reasonable length so kubectl describe output stays clean.
			tmpl.Status.Conditions[i].Message = trimMessage(message, 256)
			return
		}
	}
	tmpl.Status.Conditions = append(tmpl.Status.Conditions, metav1.Condition{
		Type:               templateConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            trimMessage(message, 256),
		LastTransitionTime: now,
	})
}

func trimMessage(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AgentTeamTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&claudev1alpha1.AgentTeamTemplate{}).
		Complete(r)
}
