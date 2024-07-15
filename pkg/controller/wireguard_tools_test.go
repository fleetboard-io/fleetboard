package controller

import (
	"testing"
)

func Test_addAnnotationToSelf(t *testing.T) {
	err := addAnnotationToSelf(nil, "", "", false)
	if err != nil {
		return
	}
}
