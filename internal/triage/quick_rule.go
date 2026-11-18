package triage

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

// QuickRuleDraft holds the editable quick-rule state before saving as a native Thunderbird filter.
type QuickRuleDraft struct {
	Name             string
	AccountID        string
	MatchAll         bool     // true = AND (every condition), false = OR (any condition)
	FromValues       []string // matched via "from contains <val>"
	SubjectValues    []string // matched via "subject contains <val>"
	Action           string   // "trash", "mark_read_trash", "mark_read", "move", "delete"
	TargetFolderPath string
}

// PreviewMatchItem represents a single email inspected by the live quick-rule preview.
type PreviewMatchItem struct {
	ID      string
	Subject string
	From    string
	Folder  string
	Matched bool
}

// QuickRuleWarning is one finding of the pre-save check.
type QuickRuleWarning struct {
	Code string // "no_conditions", "no_marked_hits", "not_all_marked", "bare_domain", "shared_domain", "short_value", "short_subject", "sender_only_single", "extra_matches"
	DE   string
	EN   string
	// Broad marks findings that make a destructive rule risky; they require an explicit
	// second confirmation before saving.
	Broad bool
}

// QuickRulePreviewReport summarizes what the current draft hits before saving.
type QuickRulePreviewReport struct {
	MarkedTotal   int
	MarkedMatched int
	ExtraTotal    int
	MarkedItems   []PreviewMatchItem
	ExtraMatches  []PreviewMatchItem
	MatchedIDs    []string // all matched IDs (marked first, then extra)
	MarkedHitIDs  []string // only marked IDs that match
	Warnings      []QuickRuleWarning
	// Destructive is true for trash/move actions.
	Destructive bool
	// RequiresConfirm is true when a destructive rule has at least one Broad warning.
	RequiresConfirm bool
	// BroadWarningDE/EN repeat the most important warning (kept for simple callers).
	BroadWarningDE string
	BroadWarningEN string
}

// ExtractEmailAddress returns the normalized lowercase address from a From header.
func ExtractEmailAddress(from string) string {
	raw := strings.TrimSpace(from)
	if raw == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(raw); err == nil && strings.TrimSpace(addr.Address) != "" {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	if i := strings.Index(raw, "<"); i >= 0 {
		if j := strings.Index(raw[i:], ">"); j > 1 {
			return strings.ToLower(strings.TrimSpace(raw[i+1 : i+j]))
		}
	}
	if strings.Contains(raw, "@") && !strings.ContainsAny(raw, " \t") {
		return strings.ToLower(raw)
	}
	return ""
}

// SplitCSVTerms parses comma- or newline-separated non-empty terms, preserving order and deduplicating case-insensitively.
func SplitCSVTerms(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		if clean == "" {
			continue
		}
		k := strings.ToLower(clean)
		if !seen[k] {
			seen[k] = true
			out = append(out, clean)
		}
	}
	return out
}

// sharedMailDomains are free-mail / shared provider domains. A bare "@domain" condition for
// them would match millions of unrelated senders, so quick rules use full addresses instead.
var sharedMailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true,
	"gmx.de": true, "gmx.net": true, "gmx.at": true, "gmx.ch": true, "gmx.com": true, "gmx.org": true,
	"web.de": true, "email.de": true, "online.de": true, "1und1.de": true,
	"outlook.com": true, "outlook.de": true, "msn.com": true,
	"icloud.com": true, "me.com": true, "mac.com": true,
	"t-online.de": true, "magenta.de": true, "freenet.de": true, "arcor.de": true, "vodafone.de": true, "kabelmail.de": true,
	"posteo.de": true, "posteo.net": true, "mailbox.org": true,
	"proton.me": true, "protonmail.com": true, "protonmail.ch": true, "pm.me": true,
	"aol.com": true, "aol.de": true, "aim.com": true,
	"yandex.com": true, "yandex.ru": true, "mail.ru": true, "zoho.com": true,
	"fastmail.com": true, "fastmail.fm": true, "tutanota.com": true, "tutanota.de": true, "tuta.io": true, "tuta.com": true,
	"hey.com": true, "mail.com": true, "gmx.us": true,
}

// sharedMailFamilies are provider names that exist under many TLDs (hotmail.de, live.co.uk, yahoo.fr …).
var sharedMailFamilies = map[string]bool{
	"hotmail": true, "live": true, "yahoo": true, "ymail": true, "outlook": true, "gmx": true, "aol": true,
	"rocketmail": true, "windowslive": true,
}

// IsSharedMailDomain reports whether domain belongs to a free-mail / shared provider.
func IsSharedMailDomain(domain string) bool {
	d := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "@."))
	if d == "" {
		return false
	}
	if sharedMailDomains[d] {
		return true
	}
	labels := strings.Split(d, ".")
	if len(labels) >= 2 && len(labels) <= 3 && sharedMailFamilies[labels[0]] {
		return true
	}
	// regional sub-domains such as de.yahoo.com / uk.yahoo.com
	if len(labels) >= 3 && sharedMailFamilies[labels[1]] {
		return true
	}
	return false
}

// QuickRuleMinSubjectLetters is the minimum number of letters a subject pattern needs to be
// specific enough for a filter.
const QuickRuleMinSubjectLetters = 4

// IsDestructiveQuickRuleAction reports actions that remove mails from the inbox.
func IsDestructiveQuickRuleAction(action string) bool {
	switch action {
	case "trash", "mark_read_trash", "delete", "move":
		return true
	}
	return false
}

// SuggestQuickRule extracts a safe 0-token Thunderbird filter draft from one or more marked
// emails. Callers should pass mails of a single account only.
func SuggestQuickRule(msgs []MessageInput, lang string) QuickRuleDraft {
	draft := QuickRuleDraft{
		Action: "trash",
	}
	if len(msgs) == 0 {
		draft.Name = quickRuleName(lang, "")
		return draft
	}
	draft.AccountID = msgs[0].AccountID

	var addrs []string
	var domains []string
	var subjects []string
	for _, m := range msgs {
		if addr := ExtractEmailAddress(m.From); addr != "" {
			addrs = append(addrs, addr)
			if dom := ExtractSenderDomain(addr); dom != "" {
				domains = append(domains, dom)
			}
		}
		if subj := strings.TrimSpace(StripSubjectPrefixes(m.Subject)); subj != "" {
			subjects = append(subjects, subj)
		}
	}
	uniqAddrs := dedupStrings(addrs)
	uniqDomains := dedupStrings(domains)
	commonSubj := extractCommonSubjectPhrase(subjects)
	allHaveAddr := len(addrs) == len(msgs)

	switch {
	case len(uniqAddrs) == 1:
		draft.FromValues = []string{uniqAddrs[0]}
		if len(msgs) >= 2 && commonSubj != "" {
			draft.SubjectValues = []string{commonSubj}
			draft.MatchAll = true
		}
	case len(uniqDomains) == 1 && allHaveAddr && !IsSharedMailDomain(uniqDomains[0]):
		draft.FromValues = []string{"@" + uniqDomains[0]}
		if commonSubj != "" {
			draft.SubjectValues = []string{commonSubj}
			draft.MatchAll = true
		}
	case len(uniqAddrs) > 1:
		// Different senders (or several people at a free-mail provider): list full addresses.
		limit := min(len(uniqAddrs), 8)
		draft.FromValues = append([]string(nil), uniqAddrs[:limit]...)
		draft.MatchAll = false
	default:
		if commonSubj != "" {
			draft.SubjectValues = []string{commonSubj}
		} else if len(subjects) == 1 && subjectLetterCount(subjects[0]) >= QuickRuleMinSubjectLetters {
			draft.SubjectValues = []string{trimRuleSubject(subjects[0])}
		}
	}

	label := ""
	switch {
	case len(draft.FromValues) > 1:
		label = draft.FromValues[0] + trLangTriage(lang, fmt.Sprintf(" + %d weitere", len(draft.FromValues)-1), fmt.Sprintf(" + %d more", len(draft.FromValues)-1))
	case len(draft.FromValues) == 1:
		label = draft.FromValues[0]
	case len(draft.SubjectValues) > 0:
		label = draft.SubjectValues[0]
	}
	draft.Name = quickRuleName(lang, label)
	return draft
}

func trLangTriage(lang, de, en string) string {
	if lang == "en" {
		return en
	}
	return de
}

func quickRuleName(lang, label string) string {
	if label == "" {
		return trLangTriage(lang, "Auto-Filter: Quick-Regel", "Auto filter: Quick rule")
	}
	return trLangTriage(lang, "Auto-Filter: ", "Auto filter: ") + label
}

func trimRuleSubject(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= 48 {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:48]))
}

// subjectLetterCount counts Unicode letters (digits, punctuation and symbols don't count).
func subjectLetterCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// trimTokenPunct strips surrounding punctuation/symbols (incl. typographic quotes and dashes)
// but keeps brackets and '#' that make tokens like "[JIRA]" or "#ops" meaningful.
func trimTokenPunct(w string) string {
	return strings.TrimFunc(w, func(r rune) bool {
		if r == '[' || r == ']' || r == '#' {
			return false
		}
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
}

var subjectStopWords = map[string]bool{
	"re": true, "fwd": true, "fw": true, "aw": true, "wg": true, "antw": true,
	"the": true, "and": true, "for": true, "with": true, "from": true, "your": true, "this": true, "that": true,
	"der": true, "die": true, "das": true, "und": true, "für": true, "von": true, "ihre": true, "deine": true,
	"eine": true, "einer": true, "nicht": true, "noch": true, "sind": true, "wird": true, "ihr": true, "dein": true,
	"hello": true, "hallo": true, "info": true, "news": true, "update": true, "updates": true,
}

// extractCommonSubjectPhrase returns a phrase (≤2 tokens) contained in EVERY subject, or "".
// Subjects should already have Re:/Fwd: prefixes removed.
func extractCommonSubjectPhrase(subjects []string) string {
	if len(subjects) < 2 {
		return ""
	}
	lowered := make([]string, len(subjects))
	for i, s := range subjects {
		lowered[i] = strings.ToLower(StripSubjectPrefixes(s))
	}
	containedInAll := func(needle string) bool {
		for _, s := range lowered {
			if !strings.Contains(s, needle) {
				return false
			}
		}
		return true
	}

	var shared []string
	for _, w := range strings.Fields(StripSubjectPrefixes(subjects[0])) {
		clean := trimTokenPunct(w)
		low := strings.ToLower(clean)
		if stripped := strings.ToLower(strings.Trim(clean, "[]#")); subjectStopWords[stripped] {
			continue
		}
		// Too short / only digits (invoice numbers, dates) would overfit or over-match.
		if subjectLetterCount(clean) < QuickRuleMinSubjectLetters {
			continue
		}
		if containedInAll(low) {
			shared = append(shared, clean)
			if len(shared) >= 2 {
				break
			}
		}
	}
	if len(shared) == 0 {
		return ""
	}
	if len(shared) > 1 {
		joined := strings.Join(shared, " ")
		if containedInAll(strings.ToLower(joined)) {
			return joined
		}
	}
	return shared[0]
}

// BuildQuickRuleConditions converts editable sender/subject lists into Thunderbird NativeConditions.
func BuildQuickRuleConditions(fromValues, subjectValues []string, matchAll bool) []NativeCondition {
	var conds []NativeCondition
	for _, v := range fromValues {
		clean := strings.TrimSpace(v)
		if clean == "" {
			continue
		}
		conds = append(conds, NativeCondition{
			Field:      "from",
			Op:         "contains",
			Value:      clean,
			BooleanAnd: matchAll,
		})
	}
	for _, v := range subjectValues {
		clean := strings.TrimSpace(v)
		if clean == "" {
			continue
		}
		conds = append(conds, NativeCondition{
			Field:      "subject",
			Op:         "contains",
			Value:      clean,
			BooleanAnd: matchAll,
		})
	}
	return conds
}

// EvaluateQuickRulePreview tests conds against the marked messages and the other candidate
// messages before saving and collects warnings about over-broad rules.
func EvaluateQuickRulePreview(conds []NativeCondition, matchAll bool, markedIDs map[string]bool, candidates []MessageInput, action string) QuickRulePreviewReport {
	var rep QuickRulePreviewReport
	matchesMsg := func(m MessageInput) bool {
		if len(conds) == 0 {
			return false
		}
		res := make([]bool, len(conds))
		for i, c := range conds {
			ok, _ := evalConditionAgainstMessage(c, m)
			res[i] = ok
		}
		return evaluateConditionChain(res, conds, matchAll)
	}

	seen := make(map[string]bool, len(candidates))
	for _, m := range candidates {
		if m.ID != "" {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
		}
		hit := matchesMsg(m)
		item := PreviewMatchItem{
			ID:      m.ID,
			Subject: m.Subject,
			From:    m.From,
			Folder:  m.Folder,
			Matched: hit,
		}
		if markedIDs[m.ID] {
			rep.MarkedTotal++
			rep.MarkedItems = append(rep.MarkedItems, item)
			if hit {
				rep.MarkedMatched++
				rep.MarkedHitIDs = append(rep.MarkedHitIDs, m.ID)
				rep.MatchedIDs = append(rep.MatchedIDs, m.ID)
			}
			continue
		}
		if hit {
			rep.ExtraTotal++
			rep.ExtraMatches = append(rep.ExtraMatches, item)
		}
	}
	for _, ex := range rep.ExtraMatches {
		rep.MatchedIDs = append(rep.MatchedIDs, ex.ID)
	}

	rep.Destructive = IsDestructiveQuickRuleAction(action)
	add := func(w QuickRuleWarning) { rep.Warnings = append(rep.Warnings, w) }

	if len(conds) == 0 {
		add(QuickRuleWarning{Code: "no_conditions",
			DE: "Bitte mindestens eine Bedingung (Absender oder Betreff) angeben.",
			EN: "Please enter at least one condition (sender or subject)."})
	} else {
		if rep.MarkedTotal > 0 && rep.MarkedMatched == 0 {
			add(QuickRuleWarning{Code: "no_marked_hits",
				DE: "Die Regel trifft keine der markierten Mails — bitte Absender/Betreff anpassen.",
				EN: "The rule matches none of the selected emails — please adjust sender/subject."})
		} else if rep.MarkedMatched < rep.MarkedTotal {
			add(QuickRuleWarning{Code: "not_all_marked",
				DE: fmt.Sprintf("Nur %d von %d markierten Mails werden erfasst; die Aktion gilt nur für diese.", rep.MarkedMatched, rep.MarkedTotal),
				EN: fmt.Sprintf("Only %d of %d selected emails match; the action applies to those only.", rep.MarkedMatched, rep.MarkedTotal)})
		}

		senderOnly := true
		for _, c := range conds {
			val := strings.TrimSpace(c.Value)
			switch strings.ToLower(c.Field) {
			case "from", "sender", "to", "toorcc", "alladdresses":
				bare := strings.TrimPrefix(val, "@")
				at := strings.LastIndex(bare, "@")
				isAddr := at > 0 && at < len(bare)-1 && strings.Contains(bare[at+1:], ".")
				switch {
				case isAddr:
					// full address: specific enough
				case IsSharedMailDomain(bare):
					add(QuickRuleWarning{Code: "shared_domain", Broad: true,
						DE: fmt.Sprintf("„%s“ ist ein Freemail-/Sammel-Anbieter — die Regel würde Mails völlig fremder Absender erfassen. Bitte vollständige Adressen verwenden.", val),
						EN: fmt.Sprintf("“%s” is a free-mail/shared provider — the rule would hit mail from unrelated senders. Please use full addresses.", val)})
				case at < 0 && strings.Contains(bare, "."):
					if !matchAll || !hasSubjectCondition(conds) {
						add(QuickRuleWarning{Code: "bare_domain", Broad: true,
							DE: fmt.Sprintf("Achtung: „%s“ erfasst eine komplette Domain ohne Betreff-Einschränkung.", val),
							EN: fmt.Sprintf("Caution: “%s” matches an entire domain without a subject restriction.", val)})
					}
				default:
					add(QuickRuleWarning{Code: "short_value", Broad: true,
						DE: fmt.Sprintf("Absender-Muster „%s“ ist sehr unspezifisch (keine vollständige Adresse oder Domain).", val),
						EN: fmt.Sprintf("Sender pattern “%s” is very unspecific (not a full address or domain).", val)})
				}
			case "subject":
				senderOnly = false
				if subjectLetterCount(val) < QuickRuleMinSubjectLetters {
					add(QuickRuleWarning{Code: "short_subject", Broad: true,
						DE: fmt.Sprintf("Betreff-Muster „%s“ ist zu kurz (mind. %d Buchstaben).", val, QuickRuleMinSubjectLetters),
						EN: fmt.Sprintf("Subject pattern “%s” is too short (at least %d letters).", val, QuickRuleMinSubjectLetters)})
				}
			default:
				senderOnly = false
			}
		}
		if rep.Destructive && senderOnly && rep.MarkedTotal == 1 {
			add(QuickRuleWarning{Code: "sender_only_single", Broad: true,
				DE: "Nur eine Mail markiert: Die Regel erfasst künftig ALLE Mails dieses Absenders.",
				EN: "Only one email selected: the rule will catch ALL future mail from this sender."})
		}
		if rep.Destructive && rep.ExtraTotal > 0 {
			add(QuickRuleWarning{Code: "extra_matches", Broad: true,
				DE: fmt.Sprintf("Die Regel erfasst auch %d weitere Mail(s) im Posteingang — bitte Preview prüfen.", rep.ExtraTotal),
				EN: fmt.Sprintf("The rule also matches %d other inbox email(s) — review the preview below.", rep.ExtraTotal)})
		}
	}

	for _, w := range rep.Warnings {
		if w.Broad && rep.Destructive {
			rep.RequiresConfirm = true
		}
	}
	if len(rep.Warnings) > 0 {
		first := rep.Warnings[0]
		// Prefer the most relevant (broad) warning for the single-line summary.
		for _, w := range rep.Warnings {
			if w.Broad || w.Code == "no_conditions" || w.Code == "no_marked_hits" {
				first = w
				break
			}
		}
		rep.BroadWarningDE, rep.BroadWarningEN = first.DE, first.EN
	}
	return rep
}

func hasSubjectCondition(conds []NativeCondition) bool {
	for _, c := range conds {
		if strings.EqualFold(c.Field, "subject") && strings.TrimSpace(c.Value) != "" {
			return true
		}
	}
	return false
}
