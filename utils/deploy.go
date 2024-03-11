package utils

import (
	"encoding/json"
	"fmt"

	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/mattbaird/jsonpatch"
	"github.com/multi-cluster-network/nauti/pkg/known"
)

// current is deployed resource, modified is changed resource.
// ignoreAdd is true if you want to ignore add action.
// The function will return the bool value to indicate whether to sync back the current object.
func ResourceNeedResync(current pkgruntime.Object, modified pkgruntime.Object, ignoreAdd bool) bool {
	currentBytes, err := json.Marshal(current)
	if err != nil {
		klog.ErrorDepth(5, fmt.Sprintf("Error marshal json: %v", err))
		return false
	}

	modifiedBytes, err := json.Marshal(modified)
	if err != nil {
		klog.ErrorDepth(5, fmt.Sprintf("Error marshal json: %v", err))
		return false
	}

	patch, err := jsonpatch.CreatePatch(currentBytes, modifiedBytes)
	if err != nil {
		klog.ErrorDepth(5, fmt.Sprintf("Error creating JSON patch: %v", err))
		return false
	}
	for _, operation := range patch {
		// filter ignored paths
		if shouldPatchBeIgnored(operation) {
			continue
		}

		switch operation.Operation {
		case "add":
			if ignoreAdd {
				continue
			} else {
				return true
			}
		case "remove", "replace":
			return true
		default:
			// skip other operations, like "copy", "move" and "test"
			continue
		}
	}

	return false
}

// shouldPatchBeIgnored used to decide if this patch operation should be ignored.
func shouldPatchBeIgnored(operation jsonpatch.JsonPatchOperation) bool {
	// some fields need to be ignore like meta.selfLink, meta.resourceVersion.
	if ContainsString(fieldsToBeIgnored(), operation.Path) {
		return true
	}
	// some sections like status section need to be ignored.
	if ContainsPrefix(sectionToBeIgnored(), operation.Path) {
		return true
	}

	return false
}

func sectionToBeIgnored() []string {
	return []string{
		known.SectionStatus,
	}
}

func fieldsToBeIgnored() []string {
	return []string{
		known.MetaGeneration,
		known.CreationTimestamp,
		known.ManagedFields,
		known.MetaUID,
		known.MetaSelflink,
		known.MetaResourceVersion,
	}
}
