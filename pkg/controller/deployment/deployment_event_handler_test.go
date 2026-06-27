package deployment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/webhook/util/configuration"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = admissionregistrationv1.AddToScheme(scheme)
	return scheme
}

func newDeployment(namespace, name string, withLabel, withRollingUpdate bool) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    make(map[string]string),
		},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
		},
	}
	if withLabel {
		dep.Labels[v1alpha1.AdvancedDeploymentControlLabel] = "true"
	}
	if withRollingUpdate {
		dep.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	}
	return dep
}

func newWebhook(name string, withTimestamp bool) *admissionregistrationv1.MutatingWebhookConfiguration {
	wh := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if withTimestamp {
		now := metav1.Now()
		wh.DeletionTimestamp = &now
	}
	return wh
}

func TestMutatingWebhookEventHandler(t *testing.T) {
	matchingDep := newDeployment("ns", "matching-dep", true, false)
	fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(matchingDep).Build()
	handler := MutatingWebhookEventHandler{Reader: fakeClient}

	testCases := []struct {
		name          string
		event         interface{}
		expectEnqueue bool
		handlerFunc   func(e interface{}, q workqueue.RateLimitingInterface)
	}{
		{
			name:          "Create: Correct webhook should NOT enqueue (timestamp is zero)",
			event:         event.CreateEvent{Object: newWebhook(configuration.MutatingWebhookConfigurationName, false)},
			expectEnqueue: false,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), e.(event.CreateEvent), q)
			},
		},
		{
			name:          "Create: Correct webhook with deletion timestamp SHOULD enqueue",
			event:         event.CreateEvent{Object: newWebhook(configuration.MutatingWebhookConfigurationName, true)},
			expectEnqueue: true,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), e.(event.CreateEvent), q)
			},
		},
		{
			name:          "Create: Wrong webhook name should NOT enqueue",
			event:         event.CreateEvent{Object: newWebhook("wrong-name", false)},
			expectEnqueue: false,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), e.(event.CreateEvent), q)
			},
		},
		{
			name:          "Create: Wrong object type should NOT enqueue",
			event:         event.CreateEvent{Object: matchingDep},
			expectEnqueue: false,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), e.(event.CreateEvent), q)
			},
		},
		{
			name:          "Update: Correct webhook should NOT enqueue (timestamp is zero)",
			event:         event.UpdateEvent{ObjectNew: newWebhook(configuration.MutatingWebhookConfigurationName, false)},
			expectEnqueue: false,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), e.(event.UpdateEvent), q) // <-- FIXED
			},
		},
		{
			name:          "Update: Correct webhook with deletion timestamp SHOULD enqueue",
			event:         event.UpdateEvent{ObjectNew: newWebhook(configuration.MutatingWebhookConfigurationName, true)},
			expectEnqueue: true,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), e.(event.UpdateEvent), q)
			},
		},
		{
			name:          "Delete: Correct webhook should enqueue",
			event:         event.DeleteEvent{Object: newWebhook(configuration.MutatingWebhookConfigurationName, false)},
			expectEnqueue: true,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Delete(context.TODO(), e.(event.DeleteEvent), q)
			},
		},
		{
			name:          "Generic: Correct webhook should NOT enqueue (timestamp is zero)",
			event:         event.GenericEvent{Object: newWebhook(configuration.MutatingWebhookConfigurationName, false)},
			expectEnqueue: false,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Generic(context.TODO(), e.(event.GenericEvent), q)
			},
		},
		{
			name:          "Generic: Correct webhook with deletion timestamp SHOULD enqueue",
			event:         event.GenericEvent{Object: newWebhook(configuration.MutatingWebhookConfigurationName, true)},
			expectEnqueue: true,
			handlerFunc: func(e interface{}, q workqueue.RateLimitingInterface) {
				handler.Generic(context.TODO(), e.(event.GenericEvent), q)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			tc.handlerFunc(tc.event, q)

			if tc.expectEnqueue {
				assert.Equal(t, 1, q.Len(), "Expected item to be enqueued")
				// Verify enqueued item matches our deployment
				item, _ := q.Get()
				req := item.(reconcile.Request)
				assert.Equal(t, "matching-dep", req.Name)
				assert.Equal(t, "ns", req.Namespace)
			} else {
				assert.Equal(t, 0, q.Len(), "Expected queue to be empty")
			}
		})
	}
}

func TestEnqueue(t *testing.T) {
	depMatch := newDeployment("ns1", "dep-match", true, false)
	depWrongStrategy := newDeployment("ns1", "dep-wrong-strategy", true, true)
	depNoLabel := newDeployment("ns2", "dep-no-label", false, false)
	depMatch2 := newDeployment("ns3", "dep-match-2", true, false)

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(
		depMatch, depWrongStrategy, depNoLabel, depMatch2,
	).Build()
	handler := MutatingWebhookEventHandler{Reader: fakeClient}

	t.Run("Enqueues multiple matching deployments", func(t *testing.T) {
		q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
		handler.enqueue(q)

		assert.Equal(t, 2, q.Len())
		// Verify both deployments are enqueued
		items := []reconcile.Request{}
		for q.Len() > 0 {
			item, _ := q.Get()
			req := item.(reconcile.Request)
			items = append(items, req)
			q.Done(item)
		}
		assert.ElementsMatch(t, []reconcile.Request{
			{NamespacedName: client.ObjectKey{Namespace: "ns1", Name: "dep-match"}},
			{NamespacedName: client.ObjectKey{Namespace: "ns3", Name: "dep-match-2"}},
		}, items)
	})

	t.Run("Does not enqueue when no deployments match", func(t *testing.T) {
		clientNoMatch := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(depWrongStrategy, depNoLabel).Build()
		handlerNoMatch := MutatingWebhookEventHandler{Reader: clientNoMatch}
		q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
		handlerNoMatch.enqueue(q)
		assert.Equal(t, 0, q.Len())
	})
}
