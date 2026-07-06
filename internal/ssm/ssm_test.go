package ssm

import (
	"reflect"
	"testing"
)

func TestCollapseListing(t *testing.T) {
	leaves := []ListEntry{
		{Name: "/myapp/db-url", Type: "String"},
		{Name: "/myapp/db-password", Type: "SecureString"},
		{Name: "/myapp/prod/host", Type: "String"},
		{Name: "/myapp/prod/port", Type: "String"},
		{Name: "/myapp/stage/host", Type: "String"},
	}

	t.Run("recursive returns all leaves unchanged", func(t *testing.T) {
		got := collapseListing("/myapp", leaves, true)
		if !reflect.DeepEqual(got, leaves) {
			t.Errorf("got %v, want %v", got, leaves)
		}
	})

	t.Run("non-recursive folds deeper paths into directories", func(t *testing.T) {
		got := collapseListing("/myapp/", leaves, false)
		want := []ListEntry{
			{Name: "/myapp/db-url", Type: "String"},
			{Name: "/myapp/db-password", Type: "SecureString"},
			{Name: "/myapp/prod/", IsDir: true},
			{Name: "/myapp/stage/", IsDir: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v\nwant %#v", got, want)
		}
	})

	t.Run("root path", func(t *testing.T) {
		got := collapseListing("/", []ListEntry{
			{Name: "/top", Type: "String"},
			{Name: "/myapp/db", Type: "String"},
		}, false)
		want := []ListEntry{
			{Name: "/top", Type: "String"},
			{Name: "/myapp/", IsDir: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v\nwant %#v", got, want)
		}
	})
}
