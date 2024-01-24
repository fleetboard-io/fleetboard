package utils

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"sort"
	"strings"
)

// Copied from k8s.io/kubernetes/pkg/utils/slice/slice.go
// and make some modifications

// CopyStrings copies the contents of the specified string slice
// into a new slice.
func CopyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	c := make([]string, len(s))
	copy(c, s)
	return c
}

// SortStrings sorts the specified string slice in place. It returns the same
// slice that was provided in order to facilitate method chaining.
func SortStrings(s []string) []string {
	sort.Strings(s)
	return s
}

// ContainsString checks if a given slice of strings contains the provided string.
func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// ContainsPrefix checks if a given slice of strings start with the provided string.
func ContainsPrefix(slice []string, s string) bool {
	for _, item := range slice {
		if strings.HasPrefix(s, item) {
			return true
		}
	}
	return false
}

// RemoveString returns a newly created []string that contains all items from slice that
// are not equal to s.
func RemoveString(slice []string, s string) []string {
	newSlice := make([]string, 0)
	for _, item := range slice {
		if item == s {
			continue
		}
		newSlice = append(newSlice, item)
	}
	if len(newSlice) == 0 {
		// Sanitize for unit tests so we don't need to distinguish empty array
		// and nil.
		newSlice = nil
	}
	return newSlice
}

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func MaxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func MinInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func SumArrayInt32(array []int32) (sum int32) {
	for _, v := range array {
		sum += v
	}
	return
}

func DerivedName(clusterID, namespace, seName string) string {
	hash := sha256.New()
	hash.Write([]byte(seName))
	hashName := strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(hash.Sum(nil)))[:10]
	return fmt.Sprintf("%s-%s-%s-%s", "derived", clusterID, namespace, hashName)
}
