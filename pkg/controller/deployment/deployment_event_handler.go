package deployment

import (
	"context"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/webhook/util/configuration"
)

type MutatingWebhookEventHandler struct {
	client.Reader
}

func (m MutatingWebhookEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	config, ok := evt.Object.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !ok || config == nil || !isKruiseRolloutMutatingConfiguration(config) || config.DeletionTimestamp.IsZero() {
		return
	}
	m.enqueue(q)
}

func (m MutatingWebhookEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	config, ok := evt.Object.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !ok || config == nil || !isKruiseRolloutMutatingConfiguration(config) || config.DeletionTimestamp.IsZero() {
		return
	}
	m.enqueue(q)
}

func (m MutatingWebhookEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	config, ok := evt.ObjectNew.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !ok || config == nil || !isKruiseRolloutMutatingConfiguration(config) || config.DeletionTimestamp.IsZero() {
		return
	}
	m.enqueue(q)
}

func (m MutatingWebhookEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	config, ok := evt.Object.(*admissionregistrationv1.MutatingWebhookConfiguration)
	if !ok || config == nil || !isKruiseRolloutMutatingConfiguration(config) {
		return
	}
	m.enqueue(q)
}

func (m MutatingWebhookEventHandler) enqueue(q workqueue.RateLimitingInterface) {
	deploymentLister := appsv1.DeploymentList{}
	err := m.List(context.TODO(), &deploymentLister, client.MatchingLabels(map[string]string{v1alpha1.AdvancedDeploymentControlLabel: "true"}))
	if err != nil {
		klog.Errorf("Failed to list deployment, error: %v", err)
	}
	for index := range deploymentLister.Items {
		if deploymentLister.Items[index].Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType {
			continue
		}
		q.Add(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&deploymentLister.Items[index])})
	}
}

func isKruiseRolloutMutatingConfiguration(object client.Object) bool {
	return object.GetName() == configuration.MutatingWebhookConfigurationName
}
