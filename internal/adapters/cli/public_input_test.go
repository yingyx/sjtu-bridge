package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestPublicFlagsFollowCapabilitySchema(t *testing.T) {
	cmd := &cobra.Command{}
	in, names := addPublicInputFlags(cmd, contracts.PublicInputSchema("learning.classrooms.available"))
	if err := cmd.Flags().Parse([]string{"--building-id", "24", "--date", "2026-10-21", "--start-section", "2", "--end-section", "4", "--limit", "5", "--cursor", "next"}); err != nil {
		t.Fatal(err)
	}
	want := contracts.ListInput{BuildingID: "24", Date: "2026-10-21", StartSection: 2, EndSection: 4, Limit: 5, Cursor: "next"}
	if !reflect.DeepEqual(*in, want) || len(names) != 6 {
		t.Fatal(*in, names)
	}
	if cmd.Flags().Lookup("query") != nil || cmd.Flags().Lookup("room-id") != nil || cmd.Flags().Lookup("kind") != nil {
		t.Fatal("unrelated input flags exposed")
	}
	list := &cobra.Command{}
	listing, _ := addPublicInputFlags(list, contracts.PublicInputSchema("learning.classrooms.list"))
	if err := list.Flags().Parse([]string{"--building-id", "24", "--room-type", "TYPE-JS", "--seat-range", "21-50", "--room-features", `{"INFO-JS-DMT":["101","102"]}`}); err != nil {
		t.Fatal(err)
	}
	if listing.BuildingID != "24" || listing.RoomType != "TYPE-JS" || listing.SeatRange != "21-50" || !reflect.DeepEqual(listing.RoomFeatures, map[string][]string{"INFO-JS-DMT": {"101", "102"}}) {
		t.Fatal(*listing)
	}
	get := &cobra.Command{}
	value, _ := addPublicInputFlags(get, contracts.PublicInputSchema("campus.notices.get"))
	if get.Flags().Lookup("limit") != nil || get.Flags().Lookup("cursor") != nil || value.Limit != 0 || get.Flags().Lookup("item-id") == nil {
		t.Fatal("single-item contract changed")
	}
	// Check that every catalog schema field has a typed CLI binding.
	for _, c := range contracts.PublicCapabilities() {
		addPublicInputFlags(&cobra.Command{}, contracts.PublicInputSchema(c.ID))
	}
}

func TestRoomFeaturesFlagRejectsAmbiguousJSON(t *testing.T) {
	var target map[string][]string
	flag := roomFeaturesFlag{target: &target}
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"INFO-A":["1"],"info-a":["2"]}`, string([]byte{0xff})} {
		if flag.Set(raw) == nil {
			t.Fatal("invalid room feature JSON accepted", raw)
		}
	}
}

func TestIDSFlagsAndJSONRemainMutuallyExclusive(t *testing.T) {
	for _, filter := range [][]string{{"--building-id", "24"}, {"--date", "2026-10-21"}, {"--start-section", "1"}, {"--end-section", "2"}} {
		args := append([]string{"learning", "classrooms", "available", "--input-json-stdin", "--json"}, filter...)
		var out, diag bytes.Buffer
		if Execute(context.Background(), args, &out, &diag, strings.NewReader(`{}`)) == 0 || out.Len() != 0 || !strings.Contains(diag.String(), "只能选择一种输入方式") || !json.Valid(diag.Bytes()) {
			t.Fatal(args, out.String(), diag.String())
		}
	}
}

func TestSchemaHelpShowsRequirementsWithoutBlockingJSON(t *testing.T) {
	cmd := &cobra.Command{}
	addPublicInputFlags(cmd, contracts.PublicInputSchema("learning.classrooms.available"))
	for name, expected := range map[string][]string{
		"building-id":   {"必填", "sjtu campus buildings list"},
		"date":          {"必填", "YYYY-MM-DD"},
		"start-section": {"必填", "整数", "1–20"},
		"limit":         {"整数", "1–100"},
	} {
		flag := cmd.Flags().Lookup(name)
		for _, text := range expected {
			if !strings.Contains(flag.Usage, text) {
				t.Fatal(name, flag.Usage)
			}
		}
		if len(flag.Annotations[cobra.BashCompOneRequiredFlag]) != 0 {
			t.Fatal("Cobra required flag blocks JSON input", name)
		}
	}
	cmd = &cobra.Command{}
	addPublicInputFlags(cmd, contracts.PublicInputSchema("life.lostfound.list"))
	if usage := cmd.Flags().Lookup("kind").Usage; !strings.Contains(usage, "looking_for_item") || !strings.Contains(usage, "looking_for_owner") {
		t.Fatal(usage)
	}
}
