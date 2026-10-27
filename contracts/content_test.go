package contracts

import (
	"strings"
	"testing"
	"time"
)

func TestContentContractValidation(t *testing.T) {
	d := ContentDescriptor{Ref: strings.Repeat("a", 64), Name: "synthetic.bin", MIMEType: "application/octet-stream", Version: "v1", Size: 0, ExpiresAt: time.Now().Add(time.Minute), ChunkSize: ContentChunkSize}
	if !d.Valid() || !(ContentRequest{Ref: d.Ref}).Valid() || !(ContentReadRequest{Ref: d.Ref, Version: d.Version, Offset: 0, Length: 1}).Valid() {
		t.Fatal("valid descriptor, empty content or range rejected")
	}
	for _, mutate := range []func(*ContentDescriptor){
		func(d *ContentDescriptor) { d.Ref = strings.Repeat("A", 64) },
		func(d *ContentDescriptor) { d.Name = "../file" },
		func(d *ContentDescriptor) { d.Name = `C:\file` },
		func(d *ContentDescriptor) { d.Name = "file\nname" },
		func(d *ContentDescriptor) { d.Name = " file" },
		func(d *ContentDescriptor) { d.Name = string([]byte{0xff}) },
		func(d *ContentDescriptor) { d.Name = strings.Repeat("x", 256) },
		func(d *ContentDescriptor) { d.Size = MaxContentBytes + 1 },
		func(d *ContentDescriptor) { d.Size = -1 },
		func(d *ContentDescriptor) { d.ChunkSize = ContentChunkSize + 1 },
		func(d *ContentDescriptor) { d.Version = "v1\r\nHeader: bad" },
		func(d *ContentDescriptor) { d.Version = strings.Repeat("x", 129) },
		func(d *ContentDescriptor) { d.Version = "v 1" },
		func(d *ContentDescriptor) { d.ExpiresAt = time.Time{} },
		func(d *ContentDescriptor) { d.MIMEType = "text/plain; charset=utf-8" },
		func(d *ContentDescriptor) { d.MIMEType = "TEXT/PLAIN" },
		func(d *ContentDescriptor) { d.MIMEType = "invalid" },
	} {
		bad := d
		mutate(&bad)
		if bad.Valid() {
			t.Fatalf("invalid content descriptor accepted: %#v", bad)
		}
	}
	for _, q := range []ContentReadRequest{
		{Ref: d.Ref, Version: d.Version, Length: 0},
		{Ref: d.Ref, Version: d.Version, Length: ContentChunkSize + 1},
		{Ref: d.Ref, Version: d.Version, Offset: -1, Length: 1},
		{Ref: d.Ref, Version: d.Version, Offset: MaxContentBytes + 1, Length: 1},
		{Ref: d.Ref, Version: "", Length: 1},
	} {
		if q.Valid() {
			t.Fatalf("invalid content range accepted: %#v", q)
		}
	}
}
