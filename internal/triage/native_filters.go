package triage

import (
	"fmt"
	"strings"

	"github.com/diver80/omathunder/internal/config"
)

type NativeCondition struct {
	Field          string `json:"field"`                    // "from", "to", "cc", "toOrCc", "allAddresses", "subject", "body", "header", "custom", "other"
	HeaderName     string `json:"headerName,omitempty"`     // e.g. "x-mailer"
	Op             string `json:"op"`                       // "contains", "doesNotContain", "is", "isNot", "beginsWith", "endsWith", "matches", "doesNotMatch"
	Value          string `json:"value"`
	BooleanAnd     bool   `json:"booleanAnd"`
	MatchAll       bool   `json:"matchAll,omitempty"`
	BeginsGrouping bool   `json:"beginsGrouping,omitempty"`
	EndsGrouping   bool   `json:"endsGrouping,omitempty"`
}

type NativeAction struct {
	Type               string `json:"type"` // "moveToFolder", "copyToFolder", "markRead", "delete", "addTag", "junk", "stopExecution", "other"
	TargetFolderURI    string `json:"targetFolderUri,omitempty"`
	TargetFolderPath   string `json:"targetFolderPath,omitempty"`
	TargetFolderExists *bool  `json:"targetFolderExists,omitempty"`
	Tag                string `json:"tag,omitempty"`
}

type NativeFilter struct {
	AccountID        string            `json:"accountId"`
	AccountName      string            `json:"accountName,omitempty"`
	Index            int               `json:"index"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	FilterType       int               `json:"filterType,omitempty"`
	MatchAll         bool              `json:"matchAll"`
	HasMixedGrouping bool              `json:"hasMixedGrouping,omitempty"`
	Conditions       []NativeCondition `json:"conditions"`
	Actions          []NativeAction    `json:"actions"`
}

type ConditionEvaluation struct {
	Field      string `json:"field"`
	HeaderName string `json:"headerName,omitempty"`
	Op         string `json:"op"`
	Value      string `json:"value"`
	Matched    bool   `json:"matched"`
	Hint       string `json:"hint,omitempty"`
}

type FilterMatchDiagnostic struct {
	AccountID           string                `json:"accountId"`
	FilterName          string                `json:"filterName"`
	Enabled             bool                  `json:"enabled"`
	MatchAll            bool                  `json:"matchAll"`
	Matched             bool                  `json:"matched"`
	WouldMatchIfEnabled bool                  `json:"wouldMatchIfEnabled,omitempty"`
	WouldMatchIfOr      bool                  `json:"wouldMatchIfOr,omitempty"`
	MatchedConditions   int                   `json:"matchedConditions"`
	TotalConditions     int                   `json:"totalConditions"`
	TargetFolderPath    string                `json:"targetFolderPath,omitempty"`
	ActionsSummary      string                `json:"actionsSummary,omitempty"`
	ReasonCode          string                `json:"reasonCode"` // "matched", "disabled", "and_partial_match", "wrong_field", "no_condition_match"
	Reason              string                `json:"reason"`
	QuickFix            string                `json:"quickFix,omitempty"` // "enable", "set_or"
	ConditionResults    []ConditionEvaluation `json:"conditionResults"`
}

type FilterIssue struct {
	AccountID  string `json:"accountId"`
	FilterName string `json:"filterName"`
	Severity   string `json:"severity"` // "error", "warn"
	Code       string `json:"code"`     // "and_mutually_exclusive", "email_in_subject", "missing_folder", "disabled_filter"
	Detail     string `json:"detail"`
	QuickFix   string `json:"quickFix,omitempty"` // "set_or", "enable"
}

type FilterDiagnosticReport struct {
	MessageAccountID string                  `json:"messageAccountId"`
	MessageSubject   string                  `json:"messageSubject"`
	MessageFrom      string                  `json:"messageFrom"`
	SenderDomain     string                  `json:"senderDomain"`
	CheckedFilters   int                     `json:"checkedFilters"`
	MatchedFilters   []FilterMatchDiagnostic `json:"matchedFilters,omitempty"`
	NearMisses       []FilterMatchDiagnostic `json:"nearMisses,omitempty"`
	Issues           []FilterIssue           `json:"issues,omitempty"`
}

type SpaceImportCandidate struct {
	AccountID         string   `json:"accountId"`
	AccountName       string   `json:"accountName,omitempty"`
	Space             string   `json:"space"`
	FolderPath        string   `json:"folderPath"`
	Domains           []string `json:"domains,omitempty"`
	FromContains      []string `json:"fromContains,omitempty"`
	SubjectContains   []string `json:"subjectContains,omitempty"`
	NativeFilterNames []string `json:"nativeFilterNames"`
	AlreadyImported   bool     `json:"alreadyImported"`
}

func evalStringOp(candidate, op, needle string) bool {
	c := strings.ToLower(strings.TrimSpace(candidate))
	n := strings.ToLower(strings.TrimSpace(needle))
	if n == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "contains":
		return strings.Contains(c, n)
	case "doesnotcontain", "doesntcontain":
		return !strings.Contains(c, n)
	case "is":
		return c == n
	case "isnot", "isnt":
		return c != n
	case "beginswith":
		return strings.HasPrefix(c, n)
	case "endswith":
		return strings.HasSuffix(c, n)
	default:
		return strings.Contains(c, n)
	}
}

func evalConditionAgainstMessage(cond NativeCondition, msg MessageInput) (matched bool, hint string) {
	switch strings.ToLower(strings.TrimSpace(cond.Field)) {
	case "from", "sender":
		// Thunderbird checks both display/raw From and extracted address.
		return evalStringOp(msg.From, cond.Op, cond.Value), ""
	case "to":
		for _, r := range msg.To {
			if evalStringOp(r, cond.Op, cond.Value) {
				return true, ""
			}
		}
		if len(msg.To) == 0 && (cond.Op == "doesNotContain" || cond.Op == "isNot") {
			return true, ""
		}
		return false, ""
	case "toorcc", "alladdresses":
		if evalStringOp(msg.From, cond.Op, cond.Value) && strings.EqualFold(cond.Field, "allAddresses") {
			return true, ""
		}
		for _, r := range msg.To {
			if evalStringOp(r, cond.Op, cond.Value) {
				return true, ""
			}
		}
		return false, ""
	case "subject":
		ok := evalStringOp(msg.Subject, cond.Op, cond.Value)
		if !ok && strings.Contains(cond.Value, "@") && evalStringOp(msg.From, cond.Op, cond.Value) {
			return false, "email_in_subject"
		}
		return ok, ""
	case "body":
		return evalStringOp(msg.BodyText, cond.Op, cond.Value), ""
	case "header":
		hKey := strings.ToLower(strings.TrimSpace(cond.HeaderName))
		for k, v := range msg.Headers {
			if strings.ToLower(strings.TrimSpace(k)) == hKey {
				return evalStringOp(v, cond.Op, cond.Value), ""
			}
		}
		if cond.Op == "doesNotContain" || cond.Op == "isNot" {
			return true, ""
		}
		return false, ""
	default:
		return false, ""
	}
}

func evaluateConditionChain(results []bool, conds []NativeCondition, matchAll bool) bool {
	if len(results) == 0 {
		return false
	}
	if matchAll {
		for _, ok := range results {
			if !ok {
				return false
			}
		}
		return true
	}
	if !hasExplicitGrouping(conds) {
		for _, ok := range results {
			if ok {
				return true
			}
		}
		return false
	}
	// Mixed AND/OR evaluation (Thunderbird evaluates OR-terms and AND-terms sequentially).
	acc := results[0]
	for i := 1; i < len(results); i++ {
		if conds[i].BooleanAnd {
			acc = acc && results[i]
		} else {
			acc = acc || results[i]
		}
	}
	return acc
}

func hasExplicitGrouping(conds []NativeCondition) bool {
	if len(conds) <= 1 {
		return false
	}
	first := conds[0].BooleanAnd
	for _, c := range conds[1:] {
		if c.BooleanAnd != first {
			return true
		}
	}
	return false
}

func summarizeFilterActions(actions []NativeAction) (targetFolder, summary string) {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		switch a.Type {
		case "moveToFolder":
			if targetFolder == "" {
				targetFolder = a.TargetFolderPath
			}
			parts = append(parts, "Move -> "+a.TargetFolderPath)
		case "copyToFolder":
			parts = append(parts, "Copy -> "+a.TargetFolderPath)
		case "markRead":
			parts = append(parts, "Mark read")
		case "delete":
			if targetFolder == "" {
				targetFolder = "Trash"
			}
			parts = append(parts, "Delete")
		case "addTag":
			parts = append(parts, "Tag: "+a.Tag)
		case "stopExecution":
			parts = append(parts, "Stop")
		default:
			if a.Type != "" {
				parts = append(parts, a.Type)
			}
		}
	}
	return targetFolder, strings.Join(parts, ", ")
}

// DiagnoseFilters deterministically evaluates a message against all native Thunderbird filters
// and reports why each relevant filter matched or narrowly missed.
func DiagnoseFilters(msg MessageInput, filters []NativeFilter) FilterDiagnosticReport {
	senderDom := ExtractSenderDomain(msg.From)
	rep := FilterDiagnosticReport{
		MessageAccountID: msg.AccountID,
		MessageSubject:   msg.Subject,
		MessageFrom:      msg.From,
		SenderDomain:     senderDom,
		Issues:           FindFilterIssues(filters, msg.AccountID),
	}

	for _, f := range filters {
		if msg.AccountID != "" && f.AccountID != "" && f.AccountID != msg.AccountID {
			continue
		}
		rep.CheckedFilters++

		condEvals := make([]ConditionEvaluation, 0, len(f.Conditions))
		boolResults := make([]bool, 0, len(f.Conditions))
		matchedCnt := 0
		wrongFieldHit := false
		senderDomainHint := false

		for _, c := range f.Conditions {
			ok, hint := evalConditionAgainstMessage(c, msg)
			if ok {
				matchedCnt++
			}
			if hint == "email_in_subject" {
				wrongFieldHit = true
			}
			if senderDom != "" && strings.Contains(strings.ToLower(c.Value), senderDom) {
				senderDomainHint = true
			}
			condEvals = append(condEvals, ConditionEvaluation{
				Field:      c.Field,
				HeaderName: c.HeaderName,
				Op:         c.Op,
				Value:      c.Value,
				Matched:    ok,
				Hint:       hint,
			})
			boolResults = append(boolResults, ok)
		}

		allEval := evaluateConditionChain(boolResults, f.Conditions, f.MatchAll)
		orEval := matchedCnt > 0
		targetFolder, actSummary := summarizeFilterActions(f.Actions)

		diag := FilterMatchDiagnostic{
			AccountID:         f.AccountID,
			FilterName:        f.Name,
			Enabled:           f.Enabled,
			MatchAll:          f.MatchAll,
			Matched:           f.Enabled && allEval,
			MatchedConditions: matchedCnt,
			TotalConditions:   len(f.Conditions),
			TargetFolderPath:  targetFolder,
			ActionsSummary:    actSummary,
			ConditionResults:  condEvals,
		}

		if f.Enabled && allEval {
			diag.ReasonCode = "matched"
			diag.Reason = fmt.Sprintf("Alle Bedingungen erfüllt (%d/%d)", matchedCnt, len(f.Conditions))
			rep.MatchedFilters = append(rep.MatchedFilters, diag)
			continue
		}

		if !f.Enabled && allEval {
			diag.WouldMatchIfEnabled = true
			diag.ReasonCode = "disabled"
			diag.Reason = "Bedingungen passen, aber der Filter ist in Thunderbird deaktiviert."
			diag.QuickFix = "enable"
			rep.NearMisses = append(rep.NearMisses, diag)
			continue
		}

		if f.MatchAll && orEval && matchedCnt < len(f.Conditions) {
			diag.WouldMatchIfOr = true
			diag.ReasonCode = "and_partial_match"
			diag.Reason = fmt.Sprintf("Steht auf UND (Match all): %d von %d Bedingungen treffen zu, mit ODER würde er greifen.", matchedCnt, len(f.Conditions))
			diag.QuickFix = "set_or"
			rep.NearMisses = append(rep.NearMisses, diag)
			continue
		}

		if wrongFieldHit {
			diag.ReasonCode = "wrong_field"
			diag.Reason = "Der Filter sucht eine E-Mail-Adresse im Betreff statt im Absenderfeld (Von)."
			rep.NearMisses = append(rep.NearMisses, diag)
			continue
		}

		if matchedCnt > 0 || senderDomainHint {
			diag.ReasonCode = "partial_or_domain"
			if matchedCnt > 0 {
				diag.Reason = fmt.Sprintf("Nur %d von %d Bedingungen erfüllt.", matchedCnt, len(f.Conditions))
			} else {
				diag.Reason = fmt.Sprintf("Domain (%s) kommt im Filter vor, aber konkrete Bedingung passt nicht.", senderDom)
			}
			rep.NearMisses = append(rep.NearMisses, diag)
		}
	}

	return rep
}

// FindFilterIssues inspects Thunderbird's native filters for common misconfigurations.
func FindFilterIssues(filters []NativeFilter, accountFilter string) []FilterIssue {
	var issues []FilterIssue
	for _, f := range filters {
		if accountFilter != "" && f.AccountID != "" && f.AccountID != accountFilter {
			continue
		}
		if !f.Enabled {
			issues = append(issues, FilterIssue{
				AccountID:  f.AccountID,
				FilterName: f.Name,
				Severity:   "warn",
				Code:       "disabled_filter",
				Detail:     "Filter ist deaktiviert.",
				QuickFix:   "enable",
			})
		}
		if f.MatchAll && len(f.Conditions) > 1 {
			subjCount := 0
			fromIsCount := 0
			for _, c := range f.Conditions {
				if strings.EqualFold(c.Field, "subject") && (c.Op == "is" || c.Op == "beginsWith" || c.Op == "endsWith" || c.Op == "contains") {
					subjCount++
				}
				if strings.EqualFold(c.Field, "from") && c.Op == "is" {
					fromIsCount++
				}
			}
			if subjCount >= 2 || fromIsCount >= 2 {
				issues = append(issues, FilterIssue{
					AccountID:  f.AccountID,
					FilterName: f.Name,
					Severity:   "error",
					Code:       "and_mutually_exclusive",
					Detail:     "UND-Verknüpfung mit mehreren Alternativen (z. B. mehrere Betreff-/Absenderwerte): kann oft nie gleichzeitig zutreffen.",
					QuickFix:   "set_or",
				})
			}
		}
		for _, c := range f.Conditions {
			if strings.EqualFold(c.Field, "subject") && strings.Contains(c.Value, "@") {
				issues = append(issues, FilterIssue{
					AccountID:  f.AccountID,
					FilterName: f.Name,
					Severity:   "error",
					Code:       "email_in_subject",
					Detail:     fmt.Sprintf("Prüft E-Mail-Adresse %q im Betreff statt im Feld 'Von'.", c.Value),
				})
			}
		}
		for _, a := range f.Actions {
			if (a.Type == "moveToFolder" || a.Type == "copyToFolder") && a.TargetFolderExists != nil && !*a.TargetFolderExists {
				issues = append(issues, FilterIssue{
					AccountID:  f.AccountID,
					FilterName: f.Name,
					Severity:   "error",
					Code:       "missing_folder",
					Detail:     fmt.Sprintf("Zielordner %q existiert nicht (mehr).", a.TargetFolderPath),
				})
			}
		}
	}
	return issues
}

func isNoiseTargetFolder(folderPath string) bool {
	low := strings.ToLower(strings.Trim(strings.TrimSpace(folderPath), "/"))
	for _, bad := range []string{"trash", "papierkorb", "deleted", "junk", "spam", "bin"} {
		if low == bad || strings.HasSuffix(low, "/"+bad) {
			return true
		}
	}
	return false
}

func deriveSpaceName(filterName, folderPath string) string {
	base := folderPath
	if idx := strings.LastIndex(base, "/"); idx >= 0 && idx+1 < len(base) {
		base = base[idx+1:]
	}
	base = strings.TrimSpace(base)
	if base != "" && !strings.EqualFold(base, "incoming") && !strings.EqualFold(base, "inbox") {
		return base
	}
	name := strings.TrimSpace(filterName)
	for _, sep := range []string{"»", "=>", "->"} {
		if idx := strings.LastIndex(name, sep); idx >= 0 {
			name = strings.TrimSpace(name[idx+len(sep):])
		}
	}
	return name
}

func dedupStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		c := strings.TrimSpace(it)
		if c == "" {
			continue
		}
		low := strings.ToLower(c)
		if !seen[low] {
			seen[low] = true
			out = append(out, c)
		}
	}
	return out
}

// SuggestSpaceRulesFromFilters builds Space overlay rule candidates from Thunderbird's
// native MoveToFolder filters so Spaces sit cleanly over native filters.
func SuggestSpaceRulesFromFilters(filters []NativeFilter, existingRules []config.RoutingRule) []SpaceImportCandidate {
	type key struct {
		accountID  string
		folderPath string
	}
	grouped := map[key]*SpaceImportCandidate{}
	var order []key

	existingByFolder := map[key]bool{}
	for _, r := range existingRules {
		fp := strings.ToLower(strings.Trim(strings.TrimSpace(r.FolderPath), "/"))
		if fp != "" {
			existingByFolder[key{accountID: r.AccountID, folderPath: fp}] = true
		}
	}

	for _, f := range filters {
		var targetPath string
		for _, a := range f.Actions {
			if a.Type == "moveToFolder" && strings.TrimSpace(a.TargetFolderPath) != "" {
				targetPath = strings.Trim(strings.TrimSpace(a.TargetFolderPath), "/")
				break
			}
		}
		if targetPath == "" || isNoiseTargetFolder(targetPath) {
			continue
		}
		k := key{accountID: f.AccountID, folderPath: strings.ToLower(targetPath)}
		cand, exists := grouped[k]
		if !exists {
			cand = &SpaceImportCandidate{
				AccountID:       f.AccountID,
				AccountName:     f.AccountName,
				Space:           deriveSpaceName(f.Name, targetPath),
				FolderPath:      targetPath,
				AlreadyImported: existingByFolder[k],
			}
			grouped[k] = cand
			order = append(order, k)
		}
		cand.NativeFilterNames = append(cand.NativeFilterNames, f.Name)
		for _, c := range f.Conditions {
			val := strings.TrimSpace(c.Value)
			if val == "" {
				continue
			}
			switch strings.ToLower(c.Field) {
			case "from", "sender", "to", "toorcc", "alladdresses":
				if strings.HasPrefix(val, "@") && !strings.ContainsAny(val[1:], " @") {
					cand.Domains = append(cand.Domains, strings.TrimPrefix(strings.ToLower(val), "@"))
				} else if at := strings.LastIndexByte(val, '@'); at == 0 && !strings.Contains(val, " ") {
					cand.Domains = append(cand.Domains, strings.ToLower(val[1:]))
				} else if strings.EqualFold(c.Field, "from") || strings.EqualFold(c.Field, "sender") {
					cand.FromContains = append(cand.FromContains, val)
				}
			case "subject":
				cand.SubjectContains = append(cand.SubjectContains, val)
			}
		}
	}

	out := make([]SpaceImportCandidate, 0, len(order))
	for _, k := range order {
		c := grouped[k]
		c.Domains = dedupStrings(c.Domains)
		c.FromContains = dedupStrings(c.FromContains)
		c.SubjectContains = dedupStrings(c.SubjectContains)
		c.NativeFilterNames = dedupStrings(c.NativeFilterNames)
		out = append(out, *c)
	}
	return out
}
