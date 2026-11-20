package triage

import "testing"

func matchPreset(p FilterRulePreset, from, subject string) bool {
	msg := MessageInput{
		ID:      "m1",
		From:    from,
		Subject: subject,
	}
	rep := EvaluateQuickRulePreview(p.Conditions, p.MatchAll, map[string]bool{"m1": true}, []MessageInput{msg}, "")
	return rep.MarkedMatched > 0
}

func TestBuiltinAtlassianAndServerPresetsPrecision(t *testing.T) {
	presets := BuiltinFilterPresets("de", "")
	if len(presets) != 3 {
		t.Fatalf("expected 3 builtin presets, got %d", len(presets))
	}
	pAccess := presets[0]
	pOps := presets[1]
	pServer := presets[2]

	// 1. Atlassian Access & App Requests: MUST match synthetic notification patterns.
	accessHits := [][2]string{
		{"Atlassian <noreply@id.atlassian.net>", "Hey Alex, can Sam Example join their team in Confluence?"},
		{"Pat Demo (CONFLUENCE) <confluence@acme.atlassian.net>", "You’ve received 2 app install requests today"},
		{"Chris Demo (JIRA) <jira@acme-uat.atlassian.net>", "You’ve received 1 app install request today"},
		{"Atlassian <noreply@id.atlassian.net>", "You have a new product request from Jamie Example."},
		{"Atlassian <noreply@id.atlassian.net>", "Approve Taylor's request for Jordan to join Jira Service Management"},
		{"Atlassian <noreply@id.atlassian.net>", "Access request: Morgan is waiting to join their team in Confluence"},
		{"Atlassian <noreply@po.atlassian.net>", "Access request for your space"},
		{"Atlassian <noreply@id.atlassian.net>", "Don't miss Riley's request to join Jira"},
		{"Atlassian <noreply@id.atlassian.net>", "Casey requested access to join Confluence"},
		{"Atlassian <noreply@id.atlassian.net>", "Review request: Drew is asking to join Jira"},
		{"Atlassian <noreply@id.atlassian.net>", "🔒 Avery requested access to Confluence"},
		{"Atlassian <noreply@id.atlassian.net>", "Quinn needs to join Jira"},
		{"Robin Demo <notifications@atlassian.net>", "Robin Demo wants to join: Cloud Migration"},
		{"Atlassian <noreply@po.atlassian.net>", "Your new App for Jira Standard trial"},
		{"Atlassian <noreply@id.atlassian.net>", "Alice invited Bob to your Atlassian site"},
		{"Atlassian <noreply@id.atlassian.net>", "1 product created outside acme"},
		{"Atlassian <noreply@id.atlassian.com>", "Demo User's account will be permanently deleted"},
		{"Atlassian <noreply@id.atlassian.net>", "Max Mustermann benötigt Zugriff auf Confluence"},
		{"Atlassian <noreply@id.atlassian.net>", "Benutzer möchte App testen in Jira"},
	}
	for _, tc := range accessHits {
		if !matchPreset(pAccess, tc[0], tc[1]) {
			t.Errorf("Atlassian access preset missed: from=%q subj=%q", tc[0], tc[1])
		}
	}

	// 2. Atlassian Backups, Sandbox & Forge Limits: MUST match.
	opsHits := [][2]string{
		{"Atlassian <noreply@po.atlassian.net>", "Automated restore completed for Confluence in acme"},
		{"Atlassian <noreply@po.atlassian.net>", "Automated restore started for Confluence in acme"},
		{"Atlassian <noreply@po.atlassian.net>", "You've used 50% of your free Forge Key-Value store write allocation for sample-app for November 2026"},
		{"Atlassian <noreply@po.atlassian.net>", "You've used 100% of your free Forge Key-Value store read allocation for sample-app for November 2026"},
		{"Atlassian <noreply@po.atlassian.net>", "Test and experiment in your sandbox"},
		{"Atlassian <noreply@po.atlassian.net>", "Your sandbox is ready to use"},
		{"Atlassian <no-reply@am.atlassian.com>", "Your sandbox will be suspended in 7 days due to inactivity"},
	}
	for _, tc := range opsHits {
		if !matchPreset(pOps, tc[0], tc[1]) {
			t.Errorf("Atlassian ops preset missed: from=%q subj=%q", tc[0], tc[1])
		}
	}

	// 3. Routine Server Mails: MUST match.
	serverHits := [][2]string{
		{"root <noreply@example.org>", "[srv01 - root <root@srv01.example.org>] 42 Ubuntu package update(s) for srv01.example.org"},
		{"root <noreply@example.org>", "[srv02 - root@srv02.example.org (root)] 18 Ubuntu package update(s) for srv02.example.org"},
		{"Cron Daemon <root@vm-app.example.org>", "Cron <root@vm-app> if test -x /usr/sbin/apticron; then /usr/sbin/apticron --cron; else true; fi"},
	}
	for _, tc := range serverHits {
		if !matchPreset(pServer, tc[0], tc[1]) {
			t.Errorf("Server routine preset missed: from=%q subj=%q", tc[0], tc[1])
		}
	}

	// 4. Normal ticket updates, support cases, 2FA, billing, human mails & server ERRORS must NEVER match.
	mustNotMatch := [][2]string{
		{"Alice Example (Jira) <jira@acme.example.com>", "[JIRA] Updates for DEMO-2526"},
		{"Vendor Support (Jira) <support@vendor.atlassian.net>", "SUPPORT-1001 Renaming Issue Types"},
		{"Support Agent <atlassian-support@atlassiansupport.atlassian.net>", "MOVE-1002 Various users missing direct page permissions (access request follow-up)"},
		{"Bob Demo (Confluence) <confluence@acme.atlassian.net>", "[Confluence] Bob Demo created \"Partner Access\""},
		{"Atlassian <noreply@id.atlassian.com>", "12345678 is your verification code"},
		{"Atlassian <no_reply@am.atlassian.com>", "Your payment has been processed for the invoice IN-000-000-001"},
		{"Atlassian <noreply@po.atlassian.net>", "Your API Token is about to expire"},
		{"Jira automation <automation@acme.atlassian.net>", "Automation flow Create Jira Space failed"},
		{"Atlassian <noreply@po.atlassian.net>", "Automated restore failed for Confluence in acme"},
		{"Colleague <colleague@example.org>", "Benutzer benötigt Zugriff auf Confluence und möchte App testen"},
		{"jira-intern@example.org", "[Acme] Abonnement: Offene Aufgaben"},
		{"root <noreply@example.org>", "[srv02 - root@srv02.example.org (root)] SMART error (SelfTest) detected on host: srv02"},
		{"root <noreply@example.org>", "[srv02 - root@srv02.example.org (root)] ERROR! - backup failed with action: backup / srv02.example.org"},
		{"root <noreply@example.org>", "[srv03 - root@srv03.example.org (root)] srv03 /dev/md/2 event: Rebuild20"},
		{"ops@vm-test.example.org", "*** SECURITY information for vm-test.example.org ***"},
	}
	for _, tc := range mustNotMatch {
		for _, p := range presets {
			if matchPreset(p, tc[0], tc[1]) {
				t.Errorf("FALSE POSITIVE: preset %q matched protected mail from=%q subj=%q", p.Name, tc[0], tc[1])
			}
		}
	}
}
