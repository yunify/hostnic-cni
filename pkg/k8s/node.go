package k8s

import (
	"context"
	"github.com/yunify/hostnic-cni/pkg/allocator"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type NodeReconciler struct {
	client.Client
	record.EventRecorder
}

func (r *NodeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	node := &corev1.Node{}

	err := K8sHelper.client.Get(context.Background(), client.ObjectKey{Name: K8sHelper.nodeName}, node)
	if err != nil {
		return ctrl.Result{}, err
	}

	vxnet := ""
	annotations := node.GetAnnotations()
	if annotations != nil {
		vxnet = annotations[AnnoHostNicVxnet]
	}
	return ctrl.Result{}, allocator.Alloc.SetCachedVxnet(vxnet)
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				old := e.Object.(*corev1.Node)

				if old.Name == K8sHelper.nodeName {
					return true
				}

				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				old := e.ObjectOld.(*corev1.Node)
				new := e.ObjectNew.(*corev1.Node)

				if new.Name != K8sHelper.nodeName {
					return false
				}

				oldVxnet := ""
				newVxnet := ""

				if old.Annotations != nil {
					oldVxnet = old.Annotations[AnnoHostNicVxnet]
				}

				if new.Annotations != nil {
					newVxnet = new.Annotations[AnnoHostNicVxnet]
				}

				if oldVxnet != newVxnet {
					return true
				}

				return false
			},
		}).Complete(r)
}
