package nativeui

import (
	"strings"
	"testing"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/backend"
)

func TestContactNameKeyMatchesTheUsualSpellingsOfOneName(t *testing.T) {
	want := "lukas carle"
	for _, n := range []string{"Carlé, Lukas", "Carlé; Lukas", "Lukas Carle", "Lukas Carlé (extern)", "'Lukas Carlé'", "  lukas   CARLÉ "} {
		if got := contactNameKey(n); got != want {
			t.Errorf("contactNameKey(%q) = %q, want %q", n, got, want)
		}
	}
	for _, n := range []string{"", "Benjamin", "max@example.com", "(Jira)"} {
		if got := contactNameKey(n); got != "" {
			t.Errorf("contactNameKey(%q) = %q, want no key", n, got)
		}
	}
}

func TestDuplicateContactGroupsStayWithinAnAddressBook(t *testing.T) {
	list := []backend.ContactRecord{
		{ID: "1", AddressBookID: "local", DisplayName: "Becker, Thomas", Email: "t.becker@a.example"},
		{ID: "2", AddressBookID: "local", DisplayName: "Anna Berg"},
		{ID: "3", AddressBookID: "local", DisplayName: "Thomas Becker", Email: "t.becker@b.example"},
		{ID: "4", AddressBookID: "google", DisplayName: "Thomas Becker"},
		{ID: "5", AddressBookID: "local", DisplayName: "Benjamin"},
		{ID: "6", AddressBookID: "local", DisplayName: "Benjamin"},
		{ID: "7", AddressBookID: "google-contacts", DisplayName: "Thomas Becker"},
		{ID: "8", AddressBookID: "google-contacts", DisplayName: "Becker, Thomas"},
	}
	g := duplicateContactGroups(list)
	if len(g) != 1 || len(g[0]) != 2 || g[0][0].ID != "1" || g[0][1].ID != "3" {
		t.Fatalf("groups = %+v", g)
	}
}

func TestContactMergeKeepsTheFullestEntryAndEveryAddress(t *testing.T) {
	group := []backend.ContactRecord{
		{ID: "a", DisplayName: "Carlé, Lukas", Email: "lukas@old.example"},
		{ID: "b", DisplayName: "Lukas Carlé", Email: "lukas@new.example", SecondEmail: "l@private.example", Phone: "+49 1", Company: "New GmbH", Notes: "Kickoff 2025"},
		{ID: "c", DisplayName: "Carlé, Lukas", Email: "lukas@mid.example", Mobile: "+49 170", Company: "Old AG", Notes: "Kickoff 2025"},
	}
	m := newContactMerge(group)
	if m.pick["_base"] != 1 {
		t.Fatalf("base = %d, want the fullest entry 1", m.pick["_base"])
	}
	m.pick["company"] = 2 // the user prefers the other company
	got, del := m.result()
	if got.ID != "b" || strings.Join(del, ",") != "a,c" {
		t.Fatalf("kept %q, deleted %v", got.ID, del)
	}
	if got.Phone != "+49 1" || got.Mobile != "+49 170" || got.Company != "Old AG" || got.DisplayName != "Lukas Carlé" {
		t.Fatalf("fields = %+v", got)
	}
	if got.Email != "lukas@new.example" || got.SecondEmail != "l@private.example" {
		t.Fatalf("emails = %q, %q", got.Email, got.SecondEmail)
	}
	if !strings.Contains(got.Notes, "lukas@old.example") || !strings.Contains(got.Notes, "lukas@mid.example") || strings.Count(got.Notes, "Kickoff 2025") != 1 {
		t.Fatalf("notes = %q", got.Notes)
	}
	// Choosing another address as the email moves the former one into its place.
	for i, em := range m.emails {
		if em == "lukas@old.example" {
			m.primary = i
		}
	}
	got, _ = m.result()
	if got.Email != "lukas@old.example" || strings.Contains(got.Notes, "lukas@old.example") || !strings.Contains(got.Notes, "lukas@new.example") {
		t.Fatalf("after choosing another email: %q / %q", got.Email, got.Notes)
	}
}

func TestMatchExtractedContactIgnoresPeopleFromQuotedMails(t *testing.T) {
	list := []ai.ExtractedContact{
		{DisplayName: "Someone Else", Email: "else@example.com", Phone: "1"},
		{DisplayName: "Carlé, Lukas", Phone: "2"},
	}
	if got := matchExtractedContact(list, []string{"lukas@example.com"}, "Lukas Carle"); got == nil || got.Phone != "2" {
		t.Fatalf("by name: %+v", got)
	}
	list = append(list, ai.ExtractedContact{Email: "LUKAS@example.com", Phone: "3"})
	if got := matchExtractedContact(list, []string{"lukas@example.com"}, "Lukas Carle"); got == nil || got.Phone != "3" {
		t.Fatalf("by email first: %+v", got)
	}
	if got := matchExtractedContact(list[:1], []string{"lukas@example.com"}, "Lukas Carle"); got != nil {
		t.Fatalf("someone else must not be used: %+v", got)
	}
}

func TestFillContactFormOnlyFillsEmptyFields(t *testing.T) {
	a := &App{}
	a.contacts.editPhone = "+49 existing"
	filled := a.fillContactForm(&ai.ExtractedContact{Phone: "+49 new", Company: "ACME\x00GmbH", Website: "javascript:alert(1)"})
	if a.contacts.editPhone != "+49 existing" || a.contacts.editCompany != "ACME GmbH" || a.contacts.editWebsite != "" {
		t.Fatalf("form = %+v", a.contacts)
	}
	if strings.Join(filled, ",") != "company" || !a.contacts.aiFilled["company"] {
		t.Fatalf("filled = %v", filled)
	}
	if got := sanitizeContactField("website", "https://acme.de/team"); got != "https://acme.de/team" {
		t.Fatalf("valid website rejected: %q", got)
	}
}
