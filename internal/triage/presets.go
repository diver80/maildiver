package triage

import (
	"fmt"
	"strings"
)

// FilterRulePreset describes a ready-to-install 1-click filter rule template.
type FilterRulePreset struct {
	ID            string
	Name          string
	Description   string
	DefaultFolder string // preferred target folder path if present in the account
	MatchAll      bool
	Conditions    []NativeCondition
	Actions       []NativeAction
}

func presetSubjOR(vals ...string) []NativeCondition {
	out := make([]NativeCondition, 0, len(vals))
	for _, v := range vals {
		out = append(out, NativeCondition{
			Field:      "subject",
			Op:         "contains",
			Value:      v,
			BooleanAnd: false,
		})
	}
	return out
}

func presetGuardAND(field, op, val string) NativeCondition {
	return NativeCondition{
		Field:      field,
		Op:         op,
		Value:      val,
		BooleanAnd: true,
	}
}

// BuiltinFilterPresets returns curated, high-precision filter templates that keep normal
// ticket updates, support cases, 2FA codes, billing notices, and error alerts untouched.
func BuiltinFilterPresets(lang string, targetFolderOverride string) []FilterRulePreset {
	isEN := strings.EqualFold(lang, "en")
	tr := func(de, en string) string {
		if isEN {
			return en
		}
		return de
	}

	targetFor := func(def string) string {
		if strings.TrimSpace(targetFolderOverride) != "" {
			return strings.TrimSpace(targetFolderOverride)
		}
		return def
	}

	// 1. Atlassian access requests, app install / trial requests, product & invite noise.
	// Uses sequential mixed grouping: (any access/app subject pattern) AND (From contains atlassian)
	// AND (not Atlassian Support, not [JIRA]/Updates/2FA verification codes).
	atlassianAccessConds := append(
		presetSubjOR(
			"join their team",
			"app install request",
			"new product request",
			"access request",
			"requested access",
			"asking to join",
			"request to join",
			"needs to join",
			"wants to join",
			"wants to try",
			"standard trial",
			"to join jira",
			"to join confluence",
			"to your atlassian site",
			"product created outside",
			"account will be permanently deleted",
			"benötigt zugriff",
			"zugriffsanfrage",
			"fordert zugriff an",
			"möchte app",
			"app testen",
			"app-installationsanfrage",
			"beitrittsanfrage",
			"möchte dem team",
			"möchte deinem team",
			"produkt angefordert",
		),
		presetGuardAND("from", "contains", "atlassian"),
		presetGuardAND("from", "doesNotContain", "atlassiansupport"),
		presetGuardAND("subject", "doesNotContain", "[JIRA]"),
		presetGuardAND("subject", "doesNotContain", "Updates for"),
		presetGuardAND("subject", "doesNotContain", "Updates für"),
		presetGuardAND("subject", "doesNotContain", "verification code"),
		presetGuardAND("subject", "doesNotContain", "Bestätigungscode"),
	)

	// 2. Atlassian automated restores, sandbox notices & Forge free-tier allocation notices.
	atlassianOpsConds := append(
		presetSubjOR(
			"automated restore started",
			"automated restore completed",
			"test and experiment in your sandbox",
			"your sandbox is ready",
			"your sandbox will be suspended",
			"free forge key-value store",
			"automatische wiederherstellung",
			"deine sandbox",
		),
		presetGuardAND("from", "contains", "atlassian"),
		presetGuardAND("from", "doesNotContain", "atlassiansupport"),
		presetGuardAND("subject", "doesNotContain", "failed"),
		presetGuardAND("subject", "doesNotContain", "fehlgeschlagen"),
		presetGuardAND("subject", "doesNotContain", "error"),
	)

	// 3. Routine Linux server package update & apticron notices (never hides SMART/RAID/Backup/Security alerts).
	serverRoutineConds := append(
		presetSubjOR(
			"ubuntu package update(s) for",
			"package update(s) for",
			"apticron",
		),
		presetGuardAND("from", "contains", "root"),
		presetGuardAND("subject", "doesNotContain", "SMART"),
		presetGuardAND("subject", "doesNotContain", "ERROR"),
		presetGuardAND("subject", "doesNotContain", "failed"),
		presetGuardAND("subject", "doesNotContain", "SECURITY"),
		presetGuardAND("subject", "doesNotContain", "/dev/md"),
	)

	mkActions := func(folder string) []NativeAction {
		return []NativeAction{
			{Type: "markRead"},
			{Type: "moveToFolder", TargetFolderPath: folder},
		}
	}

	return []FilterRulePreset{
		{
			ID:            "atlassian-access-apps",
			Name:          tr("Atlassian: Zugriffs- & App-Anfragen", "Atlassian: Access & App Requests"),
			Description:   tr("Filtert Zugriffs-, Team-Beitritts-, Produkt- & App-Installationsanfragen von Atlassian. Normale Jira-/Confluence-/Support-Ticket-Updates & 2FA-Codes laufen weiter durch.", "Filters Atlassian access, join-team, product & app install requests while keeping normal Jira/Confluence/Support ticket updates & 2FA codes."),
			DefaultFolder: "INBOX/atlassian/done",
			MatchAll:      false,
			Conditions:    atlassianAccessConds,
			Actions:       mkActions(targetFor("INBOX/atlassian/done")),
		},
		{
			ID:            "atlassian-backups-sandbox",
			Name:          tr("Atlassian: Backups, Sandbox & Forge-Limits", "Atlassian: Backups, Sandbox & Forge Limits"),
			Description:   tr("Filtert automatische Restore-Meldungen, Sandbox-Hinweise und Forge-Key-Value-Meldungen. Fehler-/Abbruchmeldungen bleiben sichtbar.", "Filters automated restore notices, sandbox reminders and Forge key-value notices while keeping failure/error alerts visible."),
			DefaultFolder: "INBOX/atlassian/done",
			MatchAll:      false,
			Conditions:    atlassianOpsConds,
			Actions:       mkActions(targetFor("INBOX/atlassian/done")),
		},
		{
			ID:            "server-routine-updates",
			Name:          tr("Server: Paketupdates & apticron", "Server: Package Updates & apticron"),
			Description:   tr("Filtert tägliche Ubuntu-Paketupdate- und apticron-Routine-Mails von root/Cron. SMART-, RAID-, Backup-Fehler & Security-Warnungen bleiben sichtbar.", "Filters daily Ubuntu package update & apticron routine emails from root/Cron while keeping SMART, RAID, backup failure & security alerts visible."),
			DefaultFolder: "INBOX/Servers/done",
			MatchAll:      false,
			Conditions:    serverRoutineConds,
			Actions:       mkActions(targetFor("INBOX/Servers/done")),
		},
	}
}

// FormatConditionChain renders a NativeFilter's conditions into a readable summary,
// accurately showing mixed AND/OR chains.
func FormatConditionChain(conds []NativeCondition, matchAll bool, lang string) string {
	if len(conds) == 0 {
		return ""
	}
	andLbl := " UND "
	orLbl := " ODER "
	if strings.EqualFold(lang, "en") {
		andLbl = " AND "
		orLbl = " OR "
	}
	mixed := !matchAll && hasExplicitGrouping(conds)
	var b strings.Builder
	for i, cd := range conds {
		if i > 0 {
			if matchAll || (mixed && cd.BooleanAnd) {
				b.WriteString(andLbl)
			} else {
				b.WriteString(orLbl)
			}
		}
		fmt.Fprintf(&b, "%s %s „%s“", cd.Field, cd.Op, cd.Value)
	}
	return b.String()
}
