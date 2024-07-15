package dedinic

import (
	"testing"
)

func Test_GetSubNetMask(t *testing.T) {
	mask, err := GetSubNetMask("20.0.0.2/16")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mask: %v", mask)
}
