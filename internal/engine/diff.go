package engine

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

//contains the resources to create, update, or delete
type ResourceDiff struct {
	ToCreate []*unstructured.Unstructured
	ToUpdate []*unstructured.Unstructured
	ToDelete []*unstructured.Unstructured
}

//computes the diff between desired and actual resources
func ComputeDiff(desired []*unstructured.Unstructured, actual []*unstructured.Unstructured) *ResourceDiff {
	diff := &ResourceDiff{
		ToCreate: make([]*unstructured.Unstructured, 0),
		ToUpdate: make([]*unstructured.Unstructured, 0),
		ToDelete: make([]*unstructured.Unstructured, 0),
	}

	actualMap := make(map[string]*unstructured.Unstructured, len(actual))
	for _, a := range actual {
		actualMap[ResourceKey(a)] = a
	}

	desiredMap := make(map[string]*unstructured.Unstructured, len(desired))
	for _, d := range desired {
		key := ResourceKey(d)
		desiredMap[key] = d

		if _, exists := actualMap[key]; exists {
			diff.ToUpdate = append(diff.ToUpdate, d)
		} else {
			diff.ToCreate = append(diff.ToCreate, d)
		}
	}

	for _, a := range actual {
		key := ResourceKey(a)
		if _, needed := desiredMap[key]; !needed {
			diff.ToDelete = append(diff.ToDelete, a)
		}
	}

	return diff
}
