package scan

import (
	"testing"

	"github.com/kubescape/k8s-interface/k8sinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/stretchr/testify/assert"
)

// A fleet scan re-points the process at each cluster in turn. Without restoring
// the original context, the process is left aimed at whichever cluster happened
// to be last in the list, and anything running afterwards silently targets the
// wrong cluster.
func TestClusterContextIsRestoredAfterEachCluster(t *testing.T) {
	original := k8sinterface.GetContextName()
	t.Cleanup(func() { k8sinterface.SetClusterContextName(original) })

	k8sinterface.SetClusterContextName("original-context")

	for _, kubeContext := range []string{"prod", "staging", "dev"} {
		func() {
			leave := cautils.EnterClusterContext(kubeContext)
			defer leave()
			assert.Equal(t, kubeContext, k8sinterface.GetContextName(), "the scan runs against its own cluster")
		}()
		assert.Equal(t, "original-context", k8sinterface.GetContextName(), "and the previous context is restored")
	}
}
