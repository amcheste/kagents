package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// pipelinePercent isn't reachable from outside the package via a named
// symbol — it's an entry in templateFuncs. Pulling it out by key
// preserves its blessed signature (function value) and lets us hit
// the boundary conditions without going through full template render.
func pipelinePercentFn() func(*claudev1alpha1.PipelineStatus) int {
	return templateFuncs["pipelinePercent"].(func(*claudev1alpha1.PipelineStatus) int)
}

func TestPipelinePercent_NilStatus(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, pipelinePercentFn()(nil))
}

func TestPipelinePercent_ZeroTotal(t *testing.T) {
	t.Parallel()
	// Division-by-zero guard: a freshly initialized PipelineStatus has
	// StagesTotal=0 before the reconciler runs the spec. Must not panic.
	assert.Equal(t, 0, pipelinePercentFn()(&claudev1alpha1.PipelineStatus{}))
}

func TestPipelinePercent_HalfComplete(t *testing.T) {
	t.Parallel()
	got := pipelinePercentFn()(&claudev1alpha1.PipelineStatus{
		StagesCompleted: 2, StagesTotal: 4,
	})
	assert.Equal(t, 50, got)
}

func TestPipelinePercent_AllComplete(t *testing.T) {
	t.Parallel()
	got := pipelinePercentFn()(&claudev1alpha1.PipelineStatus{
		StagesCompleted: 3, StagesTotal: 3,
	})
	assert.Equal(t, 100, got)
}

func TestPipelinePercent_OverflowClamps(t *testing.T) {
	t.Parallel()
	// Pathological case — reconciler bug counts more completed than
	// total. The helper should clamp rather than render a >100% bar
	// that breaks the layout.
	got := pipelinePercentFn()(&claudev1alpha1.PipelineStatus{
		StagesCompleted: 99, StagesTotal: 3,
	})
	assert.Equal(t, 100, got)
}
