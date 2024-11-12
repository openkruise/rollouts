/*
Copyright 2022 The Kruise Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
	kruisev1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruisev1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	rolloutapi "github.com/openkruise/rollouts/api"
	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	"sigs.k8s.io/yaml"
)

var k8sClient client.Client
var scheme = runtime.NewScheme()

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Kruise Rollout Resource Controller Suite", []Reporter{})
}

var _ = BeforeSuite(func(done Done) {
	By("Bootstrapping test environment")
	rand.Seed(time.Now().UnixNano())
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(GinkgoWriter)))
	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = rolloutapi.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = crdv1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = kruisev1beta1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = kruisev1alpha1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	err = gatewayv1beta1.AddToScheme(scheme)
	Expect(err).Should(BeNil())
	By("Setting up kubernetes client")
	k8sClient, err = client.New(config.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		logf.Log.Error(err, "failed to create k8sClient")
		Fail("setup failed")
	}
	By("Create the CRDs")
	var httprouteCRD crdv1.CustomResourceDefinition
	err = ReadYamlToObject("./test_data/crds/httproutes.yaml", &httprouteCRD)
	Expect(err).Should(BeNil())
	err = k8sClient.Create(context.TODO(), &httprouteCRD)
	if errors.IsAlreadyExists(err) {
		err = nil
	}
	Expect(err).Should(BeNil())

	close(done)
	By("Finished setting up test environment")
}, 300)

var _ = AfterSuite(func() {
})

// RequestReconcileNow will trigger an immediate reconciliation on K8s object.
// Some test cases may fail for timeout to wait a scheduled reconciliation.
// This is a workaround to avoid long-time wait before next scheduled
// reconciliation.
//
// deprecated: Not used, consider removing it.
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
	return Describe("[rollouts] "+text, body)
}

// KruiseDescribe is a wrapper function for ginkgo describe.  Adds namespacing.
func KruiseDescribe(text string, body func()) bool {
	return Describe("[kruise.io] "+text, body)
}
