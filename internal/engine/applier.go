package engine

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	//thisll store the list of managed resource ids on the ns.
	InventoryAnnotationKey = "namespaceclass.akuity.io/managed-resources"
)

//tracks the identity of a managed child resource
type ResourceID struct {
	GroupVersion string `json:"apiVersion"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
}

//unique identifier string for the res id
func (r ResourceID) Key() string {
	return fmt.Sprintf("%s/%s/%s/%s", r.GroupVersion, r.Kind, r.Namespace, r.Name)
}

//for executing resource creations, updates, and deletions
type Applier struct {
	client client.Client
}

//creates a new applier instance
func NewApplier(c client.Client) *Applier {
	return &Applier{client: c}
}

//executes the changes specified in the resourcediff
func (a *Applier) Apply(ctx context.Context, diff *ResourceDiff) error {
	//1.create missing resources
	for _, obj := range diff.ToCreate {
		if err := a.client.Create(ctx, obj); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed creating %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
			//if already exists, fallback to update
			diff.ToUpdate = append(diff.ToUpdate, obj)
		}
	}

	//2.update existing resources
	for _, desired := range diff.ToUpdate {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(desired.GroupVersionKind())
		key := client.ObjectKey{Namespace: desired.GetNamespace(), Name: desired.GetName()}

		if err := a.client.Get(ctx, key, existing); err != nil {
			return fmt.Errorf("failed fetching existing %s %s: %w", desired.GetKind(), desired.GetName(), err)
		}

		//ensure tracking labels are preserved
		labels := existing.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		for k, v := range desired.GetLabels() {
			labels[k] = v
		}
		existing.SetLabels(labels)

		//sync spec and data payloads
		if desiredSpec, ok := desired.Object["spec"]; ok {
			existing.Object["spec"] = desiredSpec
		}
		if desiredData, ok := desired.Object["data"]; ok {
			existing.Object["data"] = desiredData
		}

		if err := a.client.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed updating %s %s: %w", desired.GetKind(), desired.GetName(), err)
		}
	}

	//3.delete stale resources/prune
	for _, obj := range diff.ToDelete {
		if err := a.client.Delete(ctx, obj); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed pruning %s %s: %w", obj.GetKind(), obj.GetName(), err)
			}
		}
	}

	return nil
}

//fetches currently existing resources
func (a *Applier) GetExistingResources(ctx context.Context, inventory []ResourceID) ([]*unstructured.Unstructured, error) {
	existing := make([]*unstructured.Unstructured, 0, len(inventory))
	for _, item := range inventory {
		gv, err := schema.ParseGroupVersion(item.GroupVersion)
		if err != nil {
			continue
		}
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gv.WithKind(item.Kind))
		key := client.ObjectKey{Namespace: item.Namespace, Name: item.Name}

		if err := a.client.Get(ctx, key, obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed reading existing %s %s: %w", item.Kind, item.Name, err)
		}

		//verify this object is managed by this controller
		if obj.GetLabels()[ManagedByLabelKey] == ManagedByLabelValue {
			existing = append(existing, obj)
		}
	}
	return existing, nil
}

//encodes a list of resources into an inventory json string
func EncodeInventory(resources []*unstructured.Unstructured) (string, error) {
	inventory := make([]ResourceID, 0, len(resources))
	for _, r := range resources {
		inventory = append(inventory, ResourceID{
			GroupVersion: r.GetAPIVersion(),
			Kind:         r.GetKind(),
			Name:         r.GetName(),
			Namespace:    r.GetNamespace(),
		})
	}
	bytes, err := json.Marshal(inventory)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

//decodes an inventory json string into resourceids
func DecodeInventory(annotationValue string) []ResourceID {
	if annotationValue == "" {
		return nil
	}
	var inventory []ResourceID
	if err := json.Unmarshal([]byte(annotationValue), &inventory); err != nil {
		return nil
	}
	return inventory
}

//sets the inventory annotation on the nsmetadata
func UpdateNamespaceInventory(meta *metav1.ObjectMeta, resources []*unstructured.Unstructured) error {
	encoded, err := EncodeInventory(resources)
	if err != nil {
		return err
	}
	if meta.Annotations == nil {
		meta.Annotations = make(map[string]string)
	}
	meta.Annotations[InventoryAnnotationKey] = encoded
	return nil
}
