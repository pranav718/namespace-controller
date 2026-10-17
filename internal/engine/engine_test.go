package engine

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestParseManifest(t *testing.T) {
	yamlManifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
data:
  key: value
`
	raw := runtime.RawExtension{Raw: []byte(yamlManifest)}
	obj, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if obj.GetAPIVersion() != "v1" {
		t.Errorf("expected apiVersion v1, got %s", obj.GetAPIVersion())
	}
	if obj.GetKind() != "ConfigMap" {
		t.Errorf("expected kind ConfigMap, got %s", obj.GetKind())
	}
	if obj.GetName() != "test-config" {
		t.Errorf("expected name test-config, got %s", obj.GetName())
	}
}

func TestPrepareResource(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ServiceAccount")
	obj.SetName("app-sa")

	PrepareResource(obj, "test-ns", "gold-class")

	if obj.GetNamespace() != "test-ns" {
		t.Errorf("expected namespace test-ns, got %s", obj.GetNamespace())
	}

	labels := obj.GetLabels()
	if labels[ManagedByLabelKey] != ManagedByLabelValue {
		t.Errorf("expected managed-by label %s, got %s", ManagedByLabelValue, labels[ManagedByLabelKey])
	}
	if labels[ClassLabelKey] != "gold-class" {
		t.Errorf("expected class label gold-class, got %s", labels[ClassLabelKey])
	}
	if labels[NamespaceLabelKey] != "test-ns" {
		t.Errorf("expected namespace label test-ns, got %s", labels[NamespaceLabelKey])
	}
}

func TestComputeDiff(t *testing.T) {
	//desired resources
	sa1 := &unstructured.Unstructured{}
	sa1.SetAPIVersion("v1")
	sa1.SetKind("ServiceAccount")
	sa1.SetName("sa-1")
	sa1.SetNamespace("ns")

	sa2 := &unstructured.Unstructured{}
	sa2.SetAPIVersion("v1")
	sa2.SetKind("ServiceAccount")
	sa2.SetName("sa-2")
	sa2.SetNamespace("ns")

	//actual resources
	sa3 := &unstructured.Unstructured{}
	sa3.SetAPIVersion("v1")
	sa3.SetKind("ServiceAccount")
	sa3.SetName("sa-3")
	sa3.SetNamespace("ns")

	desired := []*unstructured.Unstructured{sa1, sa2}
	actual := []*unstructured.Unstructured{sa1, sa3}

	diff := ComputeDiff(desired, actual)

	if len(diff.ToCreate) != 1 || diff.ToCreate[0].GetName() != "sa-2" {
		t.Errorf("expected sa-2 to be created, got: %v", diff.ToCreate)
	}
	if len(diff.ToUpdate) != 1 || diff.ToUpdate[0].GetName() != "sa-1" {
		t.Errorf("expected sa-1 to be updated, got: %v", diff.ToUpdate)
	}
	if len(diff.ToDelete) != 1 || diff.ToDelete[0].GetName() != "sa-3" {
		t.Errorf("expected sa-3 to be deleted, got: %v", diff.ToDelete)
	}
}

func TestInventoryEncodeDecode(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")
	obj.SetName("my-config")
	obj.SetNamespace("prod")

	encoded, err := EncodeInventory([]*unstructured.Unstructured{obj})
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}

	decoded := DecodeInventory(encoded)
	if len(decoded) != 1 {
		t.Fatalf("expected 1 item, got %d", len(decoded))
	}
	if decoded[0].Name != "my-config" || decoded[0].Kind != "ConfigMap" {
		t.Errorf("unexpected decoded item: %+v", decoded[0])
	}
}

func TestApplier_Apply(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	//in memory fake client with an existing stale configmap
	staleCM := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-cm",
			Namespace: "default",
			Labels: map[string]string{
				ManagedByLabelKey: ManagedByLabelValue,
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(staleCM).Build()
	applier := NewApplier(fakeClient)

	//new desired configmap
	newCM := &unstructured.Unstructured{}
	newCM.SetAPIVersion("v1")
	newCM.SetKind("ConfigMap")
	newCM.SetName("new-cm")
	newCM.SetNamespace("default")
	PrepareResource(newCM, "default", "test-class")

	staleUnstructured := &unstructured.Unstructured{}
	staleUnstructured.SetAPIVersion("v1")
	staleUnstructured.SetKind("ConfigMap")
	staleUnstructured.SetName("stale-cm")
	staleUnstructured.SetNamespace("default")

	diff := &ResourceDiff{
		ToCreate: []*unstructured.Unstructured{newCM},
		ToDelete: []*unstructured.Unstructured{staleUnstructured},
	}

	ctx := context.Background()
	if err := applier.Apply(ctx, diff); err != nil {
		t.Fatalf("unexpected error during Apply: %v", err)
	}

	//verify newcm was created
	created := &corev1.ConfigMap{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "new-cm"}, created); err != nil {
		t.Errorf("expected new-cm to exist: %v", err)
	}

	//verify stalecm was deleted
	staleCheck := &corev1.ConfigMap{}
	err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "stale-cm"}, staleCheck)
	if err == nil {
		t.Errorf("expected stale-cm to be deleted, but it still exists")
	}
}
