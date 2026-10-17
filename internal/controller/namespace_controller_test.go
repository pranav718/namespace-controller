package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "namespace-controller/api/v1alpha1"
	"namespace-controller/internal/engine"
)

func setupTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = corev1alpha1.AddToScheme(s)
	return s
}

func TestReconcile_CreateResources(t *testing.T) {
	scheme := setupTestScheme()

	class := &corev1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "public-network",
		},
		Spec: corev1alpha1.NamespaceClassSpec{
			Resources: []runtime.RawExtension{
				{
					Raw: []byte(`{
						"apiVersion": "v1",
						"kind": "ServiceAccount",
						"metadata": {"name": "web-sa"}
					}`),
				},
			},
		},
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-portal",
			Labels: map[string]string{
				NamespaceClassLabelKey: "public-network",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(class, ns).Build()
	reconciler := &NamespaceReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Applier: engine.NewApplier(fakeClient),
	}

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "web-portal"}}

	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	//verify ServiceAccount was created in the web-portal namespace
	sa := &corev1.ServiceAccount{}
	err = fakeClient.Get(ctx, client.ObjectKey{Namespace: "web-portal", Name: "web-sa"}, sa)
	if err != nil {
		t.Fatalf("expected ServiceAccount web-sa to exist: %v", err)
	}
	if sa.Labels[engine.ManagedByLabelKey] != engine.ManagedByLabelValue {
		t.Errorf("expected managed-by label, got %s", sa.Labels[engine.ManagedByLabelKey])
	}

	//now for namespace
	updatedNS := &corev1.Namespace{}
	_ = fakeClient.Get(ctx, client.ObjectKey{Name: "web-portal"}, updatedNS)
	if updatedNS.Annotations[AppliedClassAnnotationKey] != "public-network" {
		t.Errorf("expected applied class annotation public-network, got %s", updatedNS.Annotations[AppliedClassAnnotationKey])
	}
	if updatedNS.Annotations[engine.InventoryAnnotationKey] == "" {
		t.Errorf("expected inventory annotation to be set")
	}
}

func TestReconcile_SwitchClasses(t *testing.T) {
	scheme := setupTestScheme()

	//class A: has SA "public-sa"
	classA := &corev1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "public-network"},
		Spec: corev1alpha1.NamespaceClassSpec{
			Resources: []runtime.RawExtension{
				{Raw: []byte(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"public-sa"}}`)},
			},
		},
	}

	//class B: has SA "internal-sa"
	classB := &corev1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "internal-network"},
		Spec: corev1alpha1.NamespaceClassSpec{
			Resources: []runtime.RawExtension{
				{Raw: []byte(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"internal-sa"}}`)},
			},
		},
	}

	//set namespace initlly to public-network
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-app",
			Labels: map[string]string{
				NamespaceClassLabelKey: "public-network",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(classA, classB, ns).Build()
	reconciler := &NamespaceReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Applier: engine.NewApplier(fakeClient),
	}

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "my-app"}}

	//first we will reconcile with class A
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	//now switch label to Class B
	updatedNS := &corev1.Namespace{}
	_ = fakeClient.Get(ctx, client.ObjectKey{Name: "my-app"}, updatedNS)
	updatedNS.Labels[NamespaceClassLabelKey] = "internal-network"
	_ = fakeClient.Update(ctx, updatedNS)

	//reconcile again
	_, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	//verify "internal-sa" exists
	internalSA := &corev1.ServiceAccount{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "my-app", Name: "internal-sa"}, internalSA); err != nil {
		t.Errorf("expected internal-sa to exist: %v", err)
	}

	//verify "public-sa" was deleted
	publicSA := &corev1.ServiceAccount{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "my-app", Name: "public-sa"}, publicSA); err == nil {
		t.Errorf("expected public-sa to be deleted after class switch, but it still exists")
	}
}

func TestReconcile_UnlabelNamespace(t *testing.T) {
	scheme := setupTestScheme()

	class := &corev1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "temp-class"},
		Spec: corev1alpha1.NamespaceClassSpec{
			Resources: []runtime.RawExtension{
				{Raw: []byte(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"temp-sa"}}`)},
			},
		},
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "temp-ns",
			Labels: map[string]string{
				NamespaceClassLabelKey: "temp-class",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(class, ns).Build()
	reconciler := &NamespaceReconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Applier: engine.NewApplier(fakeClient),
	}

	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "temp-ns"}}

	//first reconcile: creates resource
	_, _ = reconciler.Reconcile(ctx, req)

	//remove label
	updatedNS := &corev1.Namespace{}
	_ = fakeClient.Get(ctx, client.ObjectKey{Name: "temp-ns"}, updatedNS)
	delete(updatedNS.Labels, NamespaceClassLabelKey)
	_ = fakeClient.Update(ctx, updatedNS)

	//reconcile unlabeled namespace: should prune resources
	_, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile unlabeled namespace failed: %v", err)
	}

	sa := &corev1.ServiceAccount{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "temp-ns", Name: "temp-sa"}, sa); err == nil {
		t.Errorf("expected temp-sa to be deleted when namespace is unlabeled")
	}
}

func TestMapClassToNamespaces(t *testing.T) {
	scheme := setupTestScheme()

	ns1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-1",
			Labels: map[string]string{
				NamespaceClassLabelKey: "shared-class",
			},
		},
	}
	ns2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-2",
			Labels: map[string]string{
				NamespaceClassLabelKey: "shared-class",
			},
		},
	}
	ns3 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns-3",
			Labels: map[string]string{
				NamespaceClassLabelKey: "other-class",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1, ns2, ns3).Build()
	reconciler := &NamespaceReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	class := &corev1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-class"},
	}

	requests := reconciler.MapClassToNamespaces(context.Background(), class)
	if len(requests) != 2 {
		t.Fatalf("expected 2 reconcile requests, got %d", len(requests))
	}

	names := map[string]bool{requests[0].Name: true, requests[1].Name: true}
	if !names["ns-1"] || !names["ns-2"] {
		t.Errorf("expected ns-1 and ns-2 in requests, got %v", requests)
	}
}
