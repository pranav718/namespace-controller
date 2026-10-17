package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "namespace-controller/api/v1alpha1"
	"namespace-controller/internal/engine"
)

const (
	//label on a ns indicating its class
	NamespaceClassLabelKey = "namespaceclass.akuity.io/name"

	//tracks the last class successfully applied to this ns
	AppliedClassAnnotationKey = "namespaceclass.akuity.io/applied-class"
)

type NamespaceReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Applier *engine.Applier
}

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ns corev1.Namespace
	if err := r.Get(ctx, req.NamespacedName, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed fetching namespace: %w", err)
	}

	//skip deleted ns cuz k8s handles child deletion via ns deletion lifecycle
	if ns.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	className := ""
	if ns.Labels != nil {
		className = ns.Labels[NamespaceClassLabelKey]
	}

	//if ns has no class label/was removed
	if className == "" {
		return r.reconcileUnlabeledNamespace(ctx, &ns)
	}

	//if ns has a class
	return r.reconcileLabeledNamespace(ctx, &ns, className)
}

func (r *NamespaceReconciler) reconcileUnlabeledNamespace(ctx context.Context, ns *corev1.Namespace) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", ns.Name)

	inventory := engine.DecodeInventory(ns.Annotations[engine.InventoryAnnotationKey])
	if len(inventory) == 0 {
		return ctrl.Result{}, nil
	}

	logger.Info("Namespace is unlabeled; pruning previously managed resources", "count", len(inventory))

	existing, err := r.Applier.GetExistingResources(ctx, inventory)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed reading existing resources for cleanup: %w", err)
	}

	diff := &engine.ResourceDiff{
		ToDelete: existing,
	}

	if err := r.Applier.Apply(ctx, diff); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed pruning resources: %w", err)
	}

	delete(ns.Annotations, engine.InventoryAnnotationKey)
	delete(ns.Annotations, AppliedClassAnnotationKey)
	if err := r.Update(ctx, ns); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed clearing namespace annotations: %w", err)
	}

	logger.Info("Successfully cleaned up resources for unlabeled namespace")
	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) reconcileLabeledNamespace(ctx context.Context, ns *corev1.Namespace, className string) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", ns.Name, "class", className)

	var class corev1alpha1.NamespaceClass
	if err := r.Get(ctx, types.NamespacedName{Name: className}, &class); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Assigned NamespaceClass not found; will reconcile once created", "class", className)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed fetching NamespaceClass %s: %w", className, err)
	}

	desired, err := engine.ParseManifests(class.Spec.Resources)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed parsing resources for class %s: %w", className, err)
	}

	for _, obj := range desired {
		engine.PrepareResource(obj, ns.Name, className)
	}

	inventory := engine.DecodeInventory(ns.Annotations[engine.InventoryAnnotationKey])
	actual, err := r.Applier.GetExistingResources(ctx, inventory)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed reading existing managed resources: %w", err)
	}

	diff := engine.ComputeDiff(desired, actual)

	if err := r.Applier.Apply(ctx, diff); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed applying resource diff: %w", err)
	}

	if err := engine.UpdateNamespaceInventory(&ns.ObjectMeta, desired); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed updating namespace inventory: %w", err)
	}
	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}
	ns.Annotations[AppliedClassAnnotationKey] = className

	if err := r.Update(ctx, ns); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed updating namespace metadata: %w", err)
	}

	logger.Info("Reconciliation completed",
		"created", len(diff.ToCreate),
		"updated", len(diff.ToUpdate),
		"pruned", len(diff.ToDelete),
	)
	return ctrl.Result{}, nil
}

//maps a nsclass event to reconcile requests for all matching ns
func (r *NamespaceReconciler) MapClassToNamespaces(ctx context.Context, obj client.Object) []reconcile.Request {
	class, ok := obj.(*corev1alpha1.NamespaceClass)
	if !ok {
		return nil
	}

	var nsList corev1.NamespaceList
	err := r.List(ctx, &nsList, client.MatchingLabels{
		NamespaceClassLabelKey: class.Name,
	})
	if err != nil {
		return nil
	}

	requests := make([]reconcile.Request, len(nsList.Items))
	for i, ns := range nsList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{Name: ns.Name},
		}
	}
	return requests
}

//sets up the controller with the manager and configures watches
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Applier == nil {
		r.Applier = engine.NewApplier(r.Client)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(
			&corev1alpha1.NamespaceClass{},
			handler.EnqueueRequestsFromMapFunc(r.MapClassToNamespaces),
		).
		Complete(r)
}
