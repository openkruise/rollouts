package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	// +kubebuilder:scaffold:imports
	kruise "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
)

var k8sClient client.Client
var scheme = runtime.NewScheme()

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Kruise Rollout Resource Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	By("Bootstrapping test environment")
	rand.Seed(time.Now().UnixNano())
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter)))
	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = rolloutsv1alpha1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = crdv1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = kruise.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	By("Setting up kubernetes client")
	k8sClient, err = client.New(config.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		logf.Log.Error(err, "failed to create k8sClient")
		Fail("setup failed")
	}
	close(done)
	By("Finished setting up test environment")
}, 300)

var _ = AfterSuite(func() {
})

// RequestReconcileNow will trigger an immediate reconciliation on K8s object.
// Some test cases may fail for timeout to wait a scheduled reconciliation.
// This is a workaround to avoid long-time wait before next scheduled
// reconciliation.
func RequestReconcileNow(ctx context.Context, o client.Object) {
	oCopy := o.DeepCopyObject()
	oMeta, ok := oCopy.(metav1.Object)
	Expect(ok).Should(BeTrue())
	oMeta.SetAnnotations(map[string]string{
		"app.oam.dev/requestreconcile": time.Now().String(),
	})
	oMeta.SetResourceVersion("")
	By(fmt.Sprintf("Requset reconcile %q now", oMeta.GetName()))
	Expect(k8sClient.Patch(ctx, oCopy.(client.Object), client.Merge)).Should(Succeed())
}

// ReadYamlToObject will read a yaml K8s object to runtime.Object
func ReadYamlToObject(path string, object runtime.Object) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, object)
}

// randomNamespaceName generates a random name based on the basic name.
// Running each ginkgo case in a new namespace with a random name can avoid
// waiting a long time to GC namespace.
func randomNamespaceName(basic string) string {
	return fmt.Sprintf("%s-%s", basic, strconv.FormatInt(rand.Int63(), 16))
}

// SIGDescribe describes SIG information
func SIGDescribe(text string, body func()) bool {
	return Describe("[apps] "+text, body)
}

// KruiseDescribe is a wrapper function for ginkgo describe.  Adds namespacing.
func KruiseDescribe(text string, body func()) bool {
	return Describe("[kruise.io] "+text, body)
}
