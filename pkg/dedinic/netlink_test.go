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

//func getSubNetMask(cidr string) (string, error) {
//	subNet := strings.Split(cidr, "/")
//	if len(subNet) < 2 {
//		return "", fmt.Errorf("can not get subnet")
//	}
//	return subNet[1], nil
//}
