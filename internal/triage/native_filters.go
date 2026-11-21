package triage

import (
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

type NativeCondition struct {
	Field          string `json:"field"`                // "from", "to", "cc", "toOrCc", "allAddresses", "subject", "body", "attachment", "header", "custom", "other"
	HeaderName     string `json:"headerName,omitempty"` // e.g. "x-mailer"
	Op             string `json:"op"`                   // "contains", "doesNotContain", "is", "isNot", "beginsWith", "endsWith", "regex", "notRegex", "isEmpty", "isNotEmpty"
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

// canonicalFilterOp maps Thunderbird search operator spellings to a canonical name.
// ok=false means the operator is not supported by the local preview/diagnosis.
func canonicalFilterOp(op string) (canon string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "contains":
		return "contains", true
	case "doesnotcontain", "doesntcontain":
		return "doesNotContain", true
	case "is":
		return "is", true
	case "isnot", "isnt":
		return "isNot", true
	case "beginswith":
		return "beginsWith", true
	case "endswith":
		return "endsWith", true
	case "isempty":
		return "isEmpty", true
	case "isnotempty", "isntempty":
		return "isNotEmpty", true
	case "regex":
		return "regex", true
	case "notregex":
		return "notRegex", true
	default:
		return "", false
	}
}

const (
	maxFilterRegexLen   = 2048
	maxFilterRegexCache = 512
)

var (
	filterRegexCache   sync.Map
	filterRegexEntries atomic.Int64
	simpleHTMLTagRE    = regexp.MustCompile(`<[^>]+>`)
)

func compileFilterRegex(pattern string) (*regexp.Regexp, error) {
	pat := strings.TrimSpace(pattern)
	if pat == "" || len(pat) > maxFilterRegexLen {
		return nil, fmt.Errorf("invalid regex length")
	}
	if cached, ok := filterRegexCache.Load(pat); ok {
		if re, ok := cached.(*regexp.Regexp); ok && re != nil {
			return re, nil
		}
		return nil, fmt.Errorf("invalid regex")
	}
	re, err := regexp.Compile("(?is)" + pat)
	if filterRegexEntries.Load() >= maxFilterRegexCache {
		filterRegexCache.Range(func(k, _ any) bool {
			filterRegexCache.Delete(k)
			return true
		})
		filterRegexEntries.Store(0)
	}
	if err != nil {
		filterRegexCache.Store(pat, (*regexp.Regexp)(nil))
		filterRegexEntries.Add(1)
		return nil, err
	}
	filterRegexCache.Store(pat, re)
	filterRegexEntries.Add(1)
	return re, nil
}

func stripSimpleHTMLTags(src string) string {
	s := simpleHTMLTagRE.ReplaceAllString(src, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// isNegativeFilterOp reports operators that must hold for EVERY address/part of a header
// (Thunderbird's "match all before deciding" semantics).
func isNegativeFilterOp(canon string) bool {
	return canon == "doesNotContain" || canon == "isNot" || canon == "isEmpty" || canon == "notRegex"
}

// matchFilterString applies a canonical operator to one candidate string (case-insensitive).
func matchFilterString(candidate, canon, needle string) bool {
	c := strings.ToLower(strings.TrimSpace(candidate))
	n := strings.ToLower(strings.TrimSpace(needle))
	switch canon {
	case "isEmpty":
		return c == ""
	case "isNotEmpty":
		return c != ""
	}
	if n == "" {
		return false
	}
	switch canon {
	case "contains":
		return strings.Contains(c, n)
	case "doesNotContain":
		return !strings.Contains(c, n)
	case "is":
		return c == n
	case "isNot":
		return c != n
	case "beginsWith":
		return strings.HasPrefix(c, n)
	case "endsWith":
		return strings.HasSuffix(c, n)
	case "regex":
		re, err := compileFilterRegex(needle)
		if err != nil {
			return false
		}
		return re.MatchString(candidate)
	case "notRegex":
		re, err := compileFilterRegex(needle)
		if err != nil {
			return false
		}
		return !re.MatchString(candidate)
	default:
		return false
	}
}

// evalStringOp evaluates op on a plain string. Unknown operators never match.
func evalStringOp(candidate, op, needle string) bool {
	canon, ok := canonicalFilterOp(op)
	if !ok {
		return false
	}
	return matchFilterString(candidate, canon, needle)
}

// addressParts splits one RFC 5322 address into the parts Thunderbird matches separately:
// the mailbox address and the (decoded) display name.
func addressParts(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if addr, err := mail.ParseAddress(s); err == nil {
		parts := []string{addr.Address}
		if name := strings.TrimSpace(addr.Name); name != "" {
			parts = append(parts, name)
		}
		return parts
	}
	address := ExtractEmailAddress(s)
	name := s
	if i := strings.Index(s, "<"); i >= 0 {
		name = s[:i]
	} else if address != "" {
		name = ""
	}
	name = strings.Trim(strings.TrimSpace(name), `"' `)
	var parts []string
	if address != "" {
		parts = append(parts, address)
	}
	if name != "" {
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		parts = append(parts, s)
	}
	return parts
}

// evalAddressHeader evaluates an address condition the way Thunderbird does: every address
// is checked by its mailbox address and display name separately (never the combined
// "Name <addr>" string). Positive operators match if any part of any address matches;
// negative operators (doesNotContain/isNot) match only if all parts of all addresses satisfy
// them. An empty header satisfies negative operators only.
func evalAddressHeader(addresses []string, canon, needle string) bool {
	neg := isNegativeFilterOp(canon)
	seen := false
	for _, a := range addresses {
		for _, part := range addressParts(a) {
			seen = true
			hit := matchFilterString(part, canon, needle)
			if neg && !hit {
				return false
			}
			if !neg && hit {
				return true
			}
		}
	}
	if !seen {
		return neg || canon == "isEmpty"
	}
	return neg
}

var (
	tbReplyPrefixRE = regexp.MustCompile(`(?i)^\s*(re|aw|antw|antwort|sv|vs)\s*(\[\d+\]|\(\d+\))?\s*:\s*`)
	anyRulePrefixRE = regexp.MustCompile(`(?i)^\s*(re|aw|antw|antwort|sv|vs|fwd?|wg|tr|rv)\s*(\[\d+\]|\(\d+\))?\s*:\s*`)
)

// ThunderbirdFilterSubject returns the subject string Thunderbird's filters match against:
// reply prefixes (Re:, AW:, Antw:, SV:, Re[2]: …) are stripped and, if any was present, a
// single "Re: " is put in front. Forward prefixes (Fwd:, WG:) are part of the subject in
// Thunderbird and stay untouched.
func ThunderbirdFilterSubject(subject string) string {
	s := strings.TrimSpace(subject)
	stripped := false
	for {
		loc := tbReplyPrefixRE.FindStringIndex(s)
		if loc == nil {
			break
		}
		s = strings.TrimSpace(s[loc[1]:])
		stripped = true
	}
	if stripped {
		return "Re: " + s
	}
	return s
}

// StripSubjectPrefixes removes all reply AND forward prefixes (Re:, AW:, Fwd:, WG:, …) for
// pattern extraction.
func StripSubjectPrefixes(subject string) string {
	s := strings.TrimSpace(subject)
	for {
		loc := anyRulePrefixRE.FindStringIndex(s)
		if loc == nil {
			return s
		}
		s = strings.TrimSpace(s[loc[1]:])
	}
}

func evalConditionAgainstMessage(cond NativeCondition, msg MessageInput) (matched bool, hint string) {
	canon, known := canonicalFilterOp(cond.Op)
	if !known {
		return false, "unsupported_operator"
	}
	switch strings.ToLower(strings.TrimSpace(cond.Field)) {
	case "from", "sender":
		return evalAddressHeader([]string{msg.From}, canon, cond.Value), ""
	case "to":
		return evalAddressHeader(msg.To, canon, cond.Value), ""
	case "cc":
		return evalAddressHeader(msg.CC, canon, cond.Value), ""
	case "toorcc":
		combined := append(append([]string(nil), msg.To...), msg.CC...)
		return evalAddressHeader(combined, canon, cond.Value), ""
	case "alladdresses":
		all := make([]string, 0, len(msg.To)+len(msg.CC)+1)
		if strings.TrimSpace(msg.From) != "" {
			all = append(all, msg.From)
		}
		all = append(all, msg.To...)
		all = append(all, msg.CC...)
		return evalAddressHeader(all, canon, cond.Value), ""
	case "subject":
		ok := matchFilterString(ThunderbirdFilterSubject(msg.Subject), canon, cond.Value)
		if !ok && strings.Contains(cond.Value, "@") && evalAddressHeader([]string{msg.From}, canon, cond.Value) {
			return false, "email_in_subject"
		}
		return ok, ""
	case "body":
		body := msg.BodyText
		if strings.TrimSpace(body) == "" && strings.TrimSpace(msg.BodyHTML) != "" {
			body = stripSimpleHTMLTags(msg.BodyHTML)
		}
		return matchFilterString(body, canon, cond.Value), ""
	case "attachment", "attachments":
		if canon == "isEmpty" {
			return len(msg.Attachments) == 0, ""
		}
		if canon == "isNotEmpty" {
			return len(msg.Attachments) > 0, ""
		}
		neg := isNegativeFilterOp(canon)
		if len(msg.Attachments) == 0 {
			return neg, ""
		}
		for _, att := range msg.Attachments {
			hit := matchFilterString(att, canon, cond.Value)
			if neg && !hit {
				return false, ""
			}
			if !neg && hit {
				return true, ""
			}
		}
		return neg, ""
	case "header":
		hKey := strings.ToLower(strings.TrimSpace(cond.HeaderName))
		for k, v := range msg.Headers {
			if strings.ToLower(strings.TrimSpace(k)) == hKey {
				return matchFilterString(v, canon, cond.Value), ""
			}
		}
		if isNegativeFilterOp(canon) {
			return true, ""
		}
		return false, ""
	default:
		return false, "unsupported_field"
	}
}

func evaluateConditionChain(results []bool, conds []NativeCondition, matchAll bool) bool {
	if len(results) == 0 {
		return false
	}
	if len(results) == 1 || len(conds) != len(results) {
		if matchAll {
			for _, ok := range results {
				if !ok {
					return false
				}
			}
			return true
		}
		for _, ok := range results {
			if ok {
				return true
			}
		}
		return false
	}
	hasParens := false
	for _, c := range conds {
		if c.BeginsGrouping || c.EndsGrouping {
			hasParens = true
			break
		}
	}
	if hasParens {
		val, _ := evalGroupedConditions(results, conds, 0, matchAll)
		return val
	}
	if matchAll {
		// Plain AND unless conditions[1:] explicitly mix AND and OR.
		if !hasMixedOperators(conds) {
			for _, ok := range results {
				if !ok {
					return false
				}
			}
			return true
		}
	} else {
		// Plain OR unless any condition after the first explicitly requests AND.
		anyAnd := false
		for _, c := range conds[1:] {
			if c.BooleanAnd {
				anyAnd = true
				break
			}
		}
		if !anyAnd {
			for _, ok := range results {
				if ok {
					return true
				}
			}
			return false
		}
	}
	// Mixed AND/OR evaluation (sequential left-to-right, matching Thunderbird semantics).
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

func evalGroupedConditions(results []bool, conds []NativeCondition, start int, defaultAnd bool) (bool, int) {
	acc := false
	haveAcc := false
	i := start
	for i < len(results) {
		c := conds[i]
		joinAnd := c.BooleanAnd
		if i > start && !hasMixedOperators(conds) && !c.BooleanAnd && defaultAnd {
			joinAnd = true
		}
		var term bool
		if c.BeginsGrouping {
			// Evaluate inner group starting at current element (strip BeginsGrouping conceptually).
			innerConds := append([]NativeCondition(nil), conds...)
			innerConds[i].BeginsGrouping = false
			subVal, nextIdx := evalGroupedConditions(results, innerConds, i, defaultAnd)
			term = subVal
			i = nextIdx
		} else {
			term = results[i]
			i++
		}
		if !haveAcc {
			acc = term
			haveAcc = true
		} else if joinAnd {
			acc = acc && term
		} else {
			acc = acc || term
		}
		if c.EndsGrouping {
			return acc, i
		}
	}
	return acc, i
}

func hasMixedOperators(conds []NativeCondition) bool {
	if len(conds) <= 2 {
		return false
	}
	hasTrue, hasFalse := false, false
	for _, c := range conds[1:] {
		if c.BooleanAnd {
			hasTrue = true
		} else {
			hasFalse = true
		}
	}
	return hasTrue && hasFalse
}

func hasExplicitGrouping(conds []NativeCondition) bool {
	if len(conds) <= 1 {
		return false
	}
	for _, c := range conds {
		if c.BeginsGrouping || c.EndsGrouping {
			return true
		}
	}
	return hasMixedOperators(conds)
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
