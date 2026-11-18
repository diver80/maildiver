package main

import (
	"reflect"
	"testing"
)

func TestFilterLaunchArgsDropsProcessSerialNumber(t *testing.T) {
	got := filterLaunchArgs([]string{"-psn_0_12345", "--minimized"})
	if !reflect.DeepEqual(got, []string{"--minimized"}) {
		t.Fatalf("got %v", got)
	}
	if got := filterLaunchArgs(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
