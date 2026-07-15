package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
)

func TestRunCopyCopiesContentAndMetadata(t *testing.T) {
	ctx := context.Background()
	sourceClient := storage.NewMockClient()
	destinationClient := storage.NewMockClient()
	source := mustParse(t, "s3://source-bucket/path/source.json")
	destination := mustParse(t, "s3://destination-bucket/path/destination.json")
	wantMeta := &storage.ObjectMetadata{
		ContentType:  "application/json",
		CacheControl: "max-age=60",
		Metadata:     map[string]string{"environment": "test"},
	}
	sourceClient.PutObject(source.Bucket, source.Key, []byte(`{"enabled":true}`), wantMeta)
	destinationClient.PutObject(destination.Bucket, destination.Key, []byte(`{"enabled":false}`), &storage.ObjectMetadata{ContentType: "text/plain"})

	var output bytes.Buffer
	if err := runCopy(ctx, sourceClient, destinationClient, source, destination, &flags{yes: true}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("runCopy: %v", err)
	}

	var gotContent bytes.Buffer
	if err := destinationClient.Download(ctx, destination.Bucket, destination.Key, &gotContent); err != nil {
		t.Fatalf("downloading copied object: %v", err)
	}
	if got, want := gotContent.String(), `{"enabled":true}`; got != want {
		t.Errorf("copied content = %q, want %q", got, want)
	}
	gotMeta, err := destinationClient.HeadObject(ctx, destination.Bucket, destination.Key)
	if err != nil {
		t.Fatalf("getting copied metadata: %v", err)
	}
	if got, want := gotMeta.ContentType, wantMeta.ContentType; got != want {
		t.Errorf("ContentType = %q, want %q", got, want)
	}
	if got, want := gotMeta.CacheControl, wantMeta.CacheControl; got != want {
		t.Errorf("CacheControl = %q, want %q", got, want)
	}
	if got, want := gotMeta.Metadata["environment"], "test"; got != want {
		t.Errorf("Metadata[environment] = %q, want %q", got, want)
	}
}

func TestRunCopyDryRunDoesNotWriteDestination(t *testing.T) {
	ctx := context.Background()
	sourceClient := storage.NewMockClient()
	destinationClient := storage.NewMockClient()
	source := mustParse(t, "s3://source-bucket/source.txt")
	destination := mustParse(t, "s3://destination-bucket/destination.txt")
	sourceClient.PutObject(source.Bucket, source.Key, []byte("source"), &storage.ObjectMetadata{ContentType: "text/plain"})
	destinationClient.PutObject(destination.Bucket, destination.Key, []byte("destination"), &storage.ObjectMetadata{ContentType: "text/plain"})

	var output bytes.Buffer
	if err := runCopy(ctx, sourceClient, destinationClient, source, destination, &flags{dryRun: true}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("runCopy (--dry-run): %v", err)
	}

	var gotContent bytes.Buffer
	if err := destinationClient.Download(ctx, destination.Bucket, destination.Key, &gotContent); err != nil {
		t.Fatalf("downloading destination: %v", err)
	}
	if got, want := gotContent.String(), "destination"; got != want {
		t.Errorf("dry-run changed destination content to %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "(dry-run: copy skipped)") {
		t.Errorf("dry-run output = %q, want skip message", output.String())
	}
}

func TestRunCopyDoesNotWriteWhenSourceDownloadFails(t *testing.T) {
	ctx := context.Background()
	sourceClient := storage.NewMockClient()
	destinationClient := storage.NewMockClient()
	source := mustParse(t, "s3://source-bucket/missing.txt")
	destination := mustParse(t, "s3://destination-bucket/destination.txt")
	destinationClient.PutObject(destination.Bucket, destination.Key, []byte("destination"), &storage.ObjectMetadata{ContentType: "text/plain"})

	var output bytes.Buffer
	if err := runCopy(ctx, sourceClient, destinationClient, source, destination, &flags{yes: true}, strings.NewReader(""), &output); err == nil {
		t.Fatal("runCopy succeeded for missing source")
	}

	var gotContent bytes.Buffer
	if err := destinationClient.Download(ctx, destination.Bucket, destination.Key, &gotContent); err != nil {
		t.Fatalf("downloading destination: %v", err)
	}
	if got, want := gotContent.String(), "destination"; got != want {
		t.Errorf("source failure changed destination content to %q, want %q", got, want)
	}
}

func TestRunCopyCopiesBinaryWithoutPrintingContentDiff(t *testing.T) {
	ctx := context.Background()
	sourceClient := storage.NewMockClient()
	destinationClient := storage.NewMockClient()
	source := mustParse(t, "s3://source-bucket/source.bin")
	destination := mustParse(t, "s3://destination-bucket/destination.bin")
	binary := []byte{0x00, 0x01, 0x02, 0xff}
	sourceClient.PutObject(source.Bucket, source.Key, binary, &storage.ObjectMetadata{ContentType: "application/octet-stream"})
	destinationClient.PutObject(destination.Bucket, destination.Key, []byte("old"), &storage.ObjectMetadata{ContentType: "text/plain"})

	var output bytes.Buffer
	if err := runCopy(ctx, sourceClient, destinationClient, source, destination, &flags{yes: true}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("runCopy: %v", err)
	}

	var got bytes.Buffer
	if err := destinationClient.Download(ctx, destination.Bucket, destination.Key, &got); err != nil {
		t.Fatalf("downloading destination: %v", err)
	}
	if !bytes.Equal(got.Bytes(), binary) {
		t.Errorf("copied binary = %v, want %v", got.Bytes(), binary)
	}
	if !strings.Contains(output.String(), "Binary content; skipping content diff.") {
		t.Errorf("output = %q, want binary diff skip message", output.String())
	}
}

func TestRunCopyCreatesEmptyDestination(t *testing.T) {
	ctx := context.Background()
	sourceClient := storage.NewMockClient()
	destinationClient := storage.NewMockClient()
	source := mustParse(t, "s3://source-bucket/empty.txt")
	destination := mustParse(t, "s3://destination-bucket/empty.txt")
	sourceClient.PutObject(source.Bucket, source.Key, nil, &storage.ObjectMetadata{})

	if err := runCopy(ctx, sourceClient, destinationClient, source, destination, &flags{yes: true}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("runCopy: %v", err)
	}

	var got bytes.Buffer
	if err := destinationClient.Download(ctx, destination.Bucket, destination.Key, &got); err != nil {
		t.Fatalf("downloading created destination: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("created content length = %d, want 0", got.Len())
	}
}
