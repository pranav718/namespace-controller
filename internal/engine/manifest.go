package engine

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

const (
	ManagedByLabelKey   = "app.kubernetes.io/managed-by"
	ManagedByLabelValue = "namespaceclass-controller"

	ClassLabelKey     = "namespaceclass.akuity.io/class"
	NamespaceLabelKey = "namespaceclass.akuity.io/namespace"
)

//decodes a raw json or yaml manifest into an unstructured k8s obj
func ParseManifest(raw runtime.RawExtension) (*unstructured.Unstructured, error) {
	bytes := raw.Raw
	if len(bytes) == 0 && raw.Object != nil {
		var err error
		bytes, err = json.Marshal(raw.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal raw object: %w", err)
		}
	}

	if len(bytes) == 0 {
		return nil, fmt.Errorf("manifest contains no data")
	}

	//convert yaml to json if needed
	jsonBytes, err := yaml.YAMLToJSON(bytes)
	if err != nil {
		jsonBytes = bytes
	}

	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest into unstructured: %w", err)
	}

	if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
		return nil, fmt.Errorf("manifest missing apiVersion or kind: %s", string(bytes))
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("manifest missing metadata.name: %s", string(bytes))
	}

	return obj, nil
}

//decodes a slice of raw manifests into unstructured objs
func ParseManifests(rawList []runtime.RawExtension) ([]*unstructured.Unstructured, error) {
	result := make([]*unstructured.Unstructured, 0, len(rawList))
	for i, raw := range rawList {
		obj, err := ParseManifest(raw)
		if err != nil {
			return nil, fmt.Errorf("failed parsing resource index %d: %w", i, err)
		}
		result = append(result, obj)
	}
	return result, nil
}

//sets the target ns and stamps tracking metadata on the resource
func PrepareResource(obj *unstructured.Unstructured, targetNamespace, className string) {
	obj.SetNamespace(targetNamespace)

	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[ManagedByLabelKey] = ManagedByLabelValue
	labels[ClassLabelKey] = className
	labels[NamespaceLabelKey] = targetNamespace
	obj.SetLabels(labels)
}

//returns unique string id for an unstructured resource
func ResourceKey(obj *unstructured.Unstructured) string {
	gvk := obj.GroupVersionKind()
	return fmt.Sprintf("%s/%s/%s/%s", gvk.Group, gvk.Version, gvk.Kind, obj.GetName())
}
