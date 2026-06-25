package gcsclient

import (
	"sort"
	"strconv"

	"cloud.google.com/go/storage"
	ladlestorage "github.com/jingu/ladle/internal/storage"
)

// applyMetadata sets the writer's object attributes from the given metadata.
func applyMetadata(w *storage.Writer, meta *ladlestorage.ObjectMetadata) {
	if meta == nil {
		return
	}
	w.ContentType = meta.ContentType
	w.CacheControl = meta.CacheControl
	w.ContentEncoding = meta.ContentEncoding
	w.ContentDisposition = meta.ContentDisposition
	if len(meta.Metadata) > 0 {
		w.Metadata = copyMeta(meta.Metadata)
	}
}

// copyMeta returns a copy of the metadata map, or nil if empty.
func copyMeta(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// parseGeneration converts a version ID string to a GCS generation number.
func parseGeneration(versionID string) (int64, error) {
	return strconv.ParseInt(versionID, 10, 64)
}

// sortVersionsNewestFirst orders versions with the latest version first,
// then by most-recent modification time, matching the other backends' ordering.
func sortVersionsNewestFirst(versions []ladlestorage.ObjectVersion) {
	sort.SliceStable(versions, func(i, j int) bool {
		if versions[i].IsLatest != versions[j].IsLatest {
			return versions[i].IsLatest
		}
		return versions[i].LastModified.After(versions[j].LastModified)
	})
}
