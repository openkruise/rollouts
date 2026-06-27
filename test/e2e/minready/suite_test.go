/*
Copyright 2026 The Kruise Authors.

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

package minready

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
	crdv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	"sigs.k8s.io/yaml"

	rolloutapi "github.com/openkruise/rollouts/api"
)

var k8sClient client.Client
var scheme = runtime.NewScheme()

func TestMinReadyE2E(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Deployment MinReadySeconds E2E Suite", []Reporter{})
}

var _ = BeforeSuite(func(done Done) {
	By("Bootstrapping MinReady test environment")
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
	err = readYamlToObject("../test_data/crds/httproutes.yaml", &httprouteCRD)
	Expect(err).Should(BeNil())
	err = k8sClient.Create(context.TODO(), &httprouteCRD)
	if errors.IsAlreadyExists(err) {
		err = nil
	}
	Expect(err).Should(BeNil())

	close(done)
	By("Finished setting up MinReady test environment")
}, 300)

func readYamlToObject(path string, object runtime.Object) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, object)
}

func randomNamespaceName(basic string) string {
	return fmt.Sprintf("%s-%s", basic, strconv.FormatInt(rand.Int63(), 16))
}

func SIGDescribe(text string, body func()) bool {
	return Describe("[rollouts] "+text, body)
}

func KruiseDescribe(text string, body func()) bool {
	return Describe("[kruise.io] "+text, body)
}
