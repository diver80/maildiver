package triage

import (
	"strings"

	"github.com/diver80/omathunder/internal/config"
)

// PlannedMove is a deterministic auto-move that passed every safety check.
// In read-only mode it is only reported (dry run); otherwise the extension executes it.
type PlannedMove struct {
	MessageID       string `json:"messageId"`
	HeaderMessageID string `json:"headerMessageId"`
	AccountID       string `json:"accountId"`
	RuleID          string `json:"ruleId"`
	Customer        string `json:"customer"`
	Subject         string `json:"subject"`
	From            string `json:"from"`
	SourceFolder    string `json:"sourceFolder"`
	SourceFolderID  string `json:"sourceFolderId"`
	TargetFolder    string `json:"targetFolder"`
	TargetFolderID  string `json:"targetFolderId,omitempty"`
}

// PlanAutoMoves returns the moves allowed for the scanned messages. A mail is only moved when
//   - it lies in the INBOX of an account enabled for triage,
//   - it is unread (if the policy requires it),
//   - its sender domain matches a rule with autoMove enabled,
//   - the rule's target folder belongs to the same account (no cross-account moves),
//   - and the mail is not already inside the rule's folder tree.
//
// Folder-based matches never trigger a move: mails the user filed manually stay where they are.
func PlanAutoMoves(msgs []MessageInput, rules []config.RoutingRule, policy config.MailboxPolicy) []PlannedMove {
	planned := make([]PlannedMove, 0)
	for _, m := range msgs {
		if p, ok := planAutoMove(m, rules, policy); ok {
			planned = append(planned, p)
		}
	}
	return planned
}

func planAutoMove(m MessageInput, rules []config.RoutingRule, policy config.MailboxPolicy) (PlannedMove, bool) {
	if !m.InInbox || m.ThreadID == "" {
		return PlannedMove{}, false
	}
	if !policy.AccountEnabled(m.AccountID) {
		return PlannedMove{}, false
	}
	if policy.AutoMoveOnlyUnread && !m.Unread {
		return PlannedMove{}, false
	}
	rule, ok := MatchDomainRule(m.From, rules)
	if !ok || !rule.AutoMove {
		return PlannedMove{}, false
	}
	if rule.AccountID != "" && rule.AccountID != m.AccountID {
		return PlannedMove{}, false
	}
	target := strings.Trim(strings.TrimSpace(rule.FolderPath), "/")
	if target == "" || isSameOrSubfolder(cleanFolderPath(m.Folder), cleanFolderPath(target)) {
		return PlannedMove{}, false
	}
	targetID := ""
	if rule.AccountID == m.AccountID {
		targetID = rule.FolderID
	}
	return PlannedMove{
		MessageID:       m.ID,
		HeaderMessageID: m.ThreadID,
		AccountID:       m.AccountID,
		RuleID:          rule.ID,
		Customer:        rule.Customer,
		Subject:         m.Subject,
		From:            m.From,
		SourceFolder:    m.Folder,
		SourceFolderID:  m.FolderID,
		TargetFolder:    target,
		TargetFolderID:  targetID,
	}, true
}

func cleanFolderPath(p string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
}

func isSameOrSubfolder(folder, root string) bool {
	return folder == root || strings.HasPrefix(folder, root+"/")
}
