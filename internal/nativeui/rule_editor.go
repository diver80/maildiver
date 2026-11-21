package nativeui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/triage"
)

type ruleOptionPair struct {
	key     string
	labelDE string
	labelEN string
}

var (
	ruleFields = []ruleOptionPair{
		{key: "from", labelDE: "Von", labelEN: "From"},
		{key: "to", labelDE: "An", labelEN: "To"},
		{key: "cc", labelDE: "CC", labelEN: "CC"},
		{key: "toOrCc", labelDE: "An / CC", labelEN: "To or CC"},
		{key: "allAddresses", labelDE: "Alle Adressen", labelEN: "All addresses"},
		{key: "subject", labelDE: "Betreff", labelEN: "Subject"},
		{key: "body", labelDE: "Nachrichtentext", labelEN: "Body"},
		{key: "header", labelDE: "Header-Feld", labelEN: "Header"},
		{key: "attachment", labelDE: "Anhang-Name", labelEN: "Attachment"},
	}
	ruleOperators = []ruleOptionPair{
		{key: "contains", labelDE: "enthält", labelEN: "contains"},
		{key: "doesNotContain", labelDE: "enthält nicht", labelEN: "does not contain"},
		{key: "is", labelDE: "ist gleich", labelEN: "is"},
		{key: "isNot", labelDE: "ist nicht", labelEN: "is not"},
		{key: "beginsWith", labelDE: "beginnt mit", labelEN: "begins with"},
		{key: "endsWith", labelDE: "endet mit", labelEN: "ends with"},
		{key: "regex", labelDE: "Regex (passt)", labelEN: "matches regex"},
		{key: "notRegex", labelDE: "Regex (nicht)", labelEN: "does not match regex"},
		{key: "isEmpty", labelDE: "ist leer", labelEN: "is empty"},
		{key: "isNotEmpty", labelDE: "ist nicht leer", labelEN: "is not empty"},
	}
)

func optionLabels(pairs []ruleOptionPair, lang string) ([]string, map[string]string, map[string]string) {
	labels := make([]string, 0, len(pairs))
	keyToLabel := make(map[string]string, len(pairs))
	labelToKey := make(map[string]string, len(pairs))
	for _, p := range pairs {
		lbl := p.labelDE
		if lang == "en" {
			lbl = p.labelEN
		}
		labels = append(labels, lbl)
		keyToLabel[p.key] = lbl
		labelToKey[lbl] = p.key
	}
	return labels, keyToLabel, labelToKey
}

func (a *App) accountFolderPathsForPicker(accID string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, f := range a.folders {
		if accID != "" && f.AccountID != accID {
			continue
		}
		p := cleanFolderPath(f.Path)
		if p == "" {
			continue
		}
		low := strings.ToLower(p)
		if !seen[low] {
			seen[low] = true
			paths = append(paths, p)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.ToLower(paths[i]) < strings.ToLower(paths[j])
	})
	return paths
}

func (a *App) openRuleEditor(existing *triage.NativeFilter) {
	d := &a.direct
	d.rulesErr = ""
	d.ruleEditOpen = true
	d.ruleEditPreviewDone = false
	d.ruleEditPreviewCount = 0
	d.ruleEditPreviewTotal = 0
	d.ruleEditPreviewHits = nil
	d.ruleEditAccountMap = make(map[string]string)
	var accLabels []string
	for _, acc := range a.accounts {
		lbl := acc.Name
		if lbl == "" {
			lbl = acc.ID
		}
		d.ruleEditAccountMap[lbl] = acc.ID
		accLabels = append(accLabels, lbl)
	}
	if len(accLabels) == 0 {
		for _, acc := range d.accounts {
			lbl := acc.Email
			if lbl == "" {
				lbl = acc.AccountID
			}
			d.ruleEditAccountMap[lbl] = acc.AccountID
			accLabels = append(accLabels, lbl)
		}
	}

	if existing == nil {
		d.ruleEditOrigName = ""
		d.ruleEditOrigAccID = ""
		d.ruleEditName = ""
		d.ruleEditEnabled = true
		d.ruleEditMatchMode = 0
		d.ruleEditConds = []triage.NativeCondition{{Field: "from", Op: "contains", Value: "", BooleanAnd: true}}
		d.ruleEditMarkRead = false
		d.ruleEditAddStar = false
		d.ruleEditMove = true
		d.ruleEditMoveFolder = "Archives"
		d.ruleEditCopy = false
		d.ruleEditCopyFolder = ""
		d.ruleEditJunk = false
		d.ruleEditDelete = false
		d.ruleEditStop = false
		targetAcc := a.presetAccountID()
		d.ruleEditAccountLbl = ""
		for _, lbl := range accLabels {
			if d.ruleEditAccountMap[lbl] == targetAcc || d.ruleEditAccountLbl == "" {
				d.ruleEditAccountLbl = lbl
			}
		}
		return
	}

	d.ruleEditOrigName = existing.Name
	d.ruleEditOrigAccID = existing.AccountID
	d.ruleEditName = existing.Name
	d.ruleEditEnabled = existing.Enabled
	if existing.MatchAll {
		d.ruleEditMatchMode = 0
	} else {
		d.ruleEditMatchMode = 1
	}
	d.ruleEditConds = make([]triage.NativeCondition, len(existing.Conditions))
	copy(d.ruleEditConds, existing.Conditions)
	if len(d.ruleEditConds) == 0 {
		d.ruleEditConds = []triage.NativeCondition{{Field: "from", Op: "contains", Value: "", BooleanAnd: true}}
	}
	d.ruleEditMarkRead = false
	d.ruleEditAddStar = false
	d.ruleEditMove = false
	d.ruleEditMoveFolder = ""
	d.ruleEditCopy = false
	d.ruleEditCopyFolder = ""
	d.ruleEditJunk = false
	d.ruleEditDelete = false
	d.ruleEditStop = false
	for _, ac := range existing.Actions {
		switch ac.Type {
		case "markRead":
			d.ruleEditMarkRead = true
		case "addStar":
			d.ruleEditAddStar = true
		case "moveToFolder":
			d.ruleEditMove = true
			d.ruleEditMoveFolder = ac.TargetFolderPath
		case "copyToFolder":
			d.ruleEditCopy = true
			d.ruleEditCopyFolder = ac.TargetFolderPath
		case "junk":
			d.ruleEditJunk = true
		case "delete":
			d.ruleEditDelete = true
		case "stopExecution":
			d.ruleEditStop = true
		}
	}
	d.ruleEditAccountLbl = ""
	for _, lbl := range accLabels {
		if d.ruleEditAccountMap[lbl] == existing.AccountID {
			d.ruleEditAccountLbl = lbl
			break
		}
	}
	if d.ruleEditAccountLbl == "" && len(accLabels) > 0 {
		d.ruleEditAccountLbl = accLabels[0]
	}
}

func (a *App) sanitizedRuleConditions() []triage.NativeCondition {
	var conds []triage.NativeCondition
	for _, cd := range a.direct.ruleEditConds {
		op := strings.TrimSpace(cd.Op)
		if op == "" {
			op = "contains"
		}
		val := strings.TrimSpace(cd.Value)
		if val == "" && op != "isEmpty" && op != "isNotEmpty" {
			continue
		}
		field := strings.TrimSpace(cd.Field)
		if field == "" {
			field = "from"
		}
		headerName := strings.TrimSpace(cd.HeaderName)
		if field == "header" && headerName == "" {
			continue
		}
		conds = append(conds, triage.NativeCondition{
			Field:      field,
			HeaderName: headerName,
			Op:         op,
			Value:      val,
			BooleanAnd: cd.BooleanAnd,
		})
	}
	return conds
}

func (a *App) runRuleEditorPreview() {
	d := &a.direct
	conds := a.sanitizedRuleConditions()
	if len(conds) == 0 {
		d.ruleEditPreviewDone = true
		d.ruleEditPreviewCount = 0
		d.ruleEditPreviewTotal = len(a.messages)
		d.ruleEditPreviewHits = nil
		return
	}
	accID := d.ruleEditAccountMap[d.ruleEditAccountLbl]
	if accID == "" {
		accID = d.ruleEditOrigAccID
	}
	inputs := make([]triage.MessageInput, 0, len(a.messages))
	marked := make(map[string]bool, len(a.messages))
	byID := make(map[string]backend.MessageHeader, len(a.messages))
	for _, m := range a.messages {
		if accID != "" && m.AccountID != accID {
			continue
		}
		marked[m.ID] = true
		byID[m.ID] = m
		inputs = append(inputs, triage.MessageInput{
			ID:        m.ID,
			AccountID: m.AccountID,
			Folder:    m.Folder,
			Subject:   m.Subject,
			From:      m.From,
			To:        m.To,
			CC:        m.CC,
			Date:      m.Date,
			Unread:    m.Unread,
		})
	}
	rep := triage.EvaluateQuickRulePreview(conds, d.ruleEditMatchMode == 0, marked, inputs, accID)
	d.ruleEditPreviewDone = true
	d.ruleEditPreviewCount = rep.MarkedMatched
	d.ruleEditPreviewTotal = rep.MarkedTotal
	d.ruleEditPreviewHits = nil
	for _, sample := range rep.MarkedItems {
		if !sample.Matched {
			continue
		}
		line := sample.From
		if sample.Subject != "" {
			line += " — " + sample.Subject
		}
		d.ruleEditPreviewHits = append(d.ruleEditPreviewHits, line)
		if len(d.ruleEditPreviewHits) >= 4 {
			break
		}
	}
}

func (a *App) buildRuleEditorRequest() (backend.FilterUpsertRequest, error) {
	d := &a.direct
	name := strings.TrimSpace(d.ruleEditName)
	if name == "" {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte einen Regelnamen eingeben.", "Please enter a rule name."))
	}
	accID := d.ruleEditAccountMap[d.ruleEditAccountLbl]
	if accID == "" {
		accID = d.ruleEditOrigAccID
	}
	if accID == "" {
		accID = a.presetAccountID()
	}
	if accID == "" {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Kein E-Mail-Konto ausgewählt.", "No email account selected."))
	}
	conds := a.sanitizedRuleConditions()
	if len(conds) == 0 {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte mindestens eine gültige Bedingung angeben (bei Header auch den Header-Namen).", "Please provide at least one valid condition (including header name for Header rules)."))
	}
	var actions []backend.FilterActionSpec
	if d.ruleEditMarkRead {
		actions = append(actions, backend.FilterActionSpec{Type: "markRead"})
	}
	if d.ruleEditAddStar {
		actions = append(actions, backend.FilterActionSpec{Type: "addStar"})
	}
	if d.ruleEditCopy {
		folder := strings.TrimSpace(d.ruleEditCopyFolder)
		if folder == "" {
			return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte einen Zielordner für Kopie angeben.", "Please specify a target folder for copy."))
		}
		actions = append(actions, backend.FilterActionSpec{Type: "copyToFolder", TargetFolderPath: folder})
	}
	if d.ruleEditMove {
		folder := strings.TrimSpace(d.ruleEditMoveFolder)
		if folder == "" {
			return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte einen Zielordner angeben.", "Please enter a target folder."))
		}
		actions = append(actions, backend.FilterActionSpec{Type: "moveToFolder", TargetFolderPath: folder})
	}
	if d.ruleEditJunk {
		actions = append(actions, backend.FilterActionSpec{Type: "junk"})
	}
	if d.ruleEditDelete {
		actions = append(actions, backend.FilterActionSpec{Type: "delete"})
	}
	if d.ruleEditStop {
		actions = append(actions, backend.FilterActionSpec{Type: "stopExecution"})
	}
	if len(actions) == 0 {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte mindestens eine Aktion auswählen.", "Please select at least one action."))
	}
	mode := "create"
	if d.ruleEditOrigName != "" && strings.EqualFold(d.ruleEditOrigName, name) && d.ruleEditOrigAccID == accID {
		mode = "update"
	}
	return backend.FilterUpsertRequest{
		AccountID:    accID,
		Name:         name,
		ExistingName: d.ruleEditOrigName,
		Enabled:      d.ruleEditEnabled,
		MatchAll:     d.ruleEditMatchMode == 0,
		Conditions:   conds,
		Actions:      actions,
		Mode:         mode,
	}, nil
}

func (a *App) saveRuleEditor(ctx context.Context) {
	d := &a.direct
	req, err := a.buildRuleEditorRequest()
	if err != nil {
		d.rulesErr = err.Error()
		return
	}
	bc, cerr := a.client()
	if cerr != nil {
		d.rulesErr = a.errText(cerr)
		return
	}
	origName := d.ruleEditOrigName
	origAcc := d.ruleEditOrigAccID
	renamed := origName != "" && (!strings.EqualFold(origName, req.Name) || origAcc != req.AccountID)

	d.rulesBusy = true
	d.rulesErr = ""
	go func() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, uerr := bc.FilterUpsert(c, req)
		if uerr == nil && renamed && a.opts.Direct != nil {
			_ = a.opts.Direct.DeleteFilter(c, origAcc, origName)
		}
		a.emitUpdate(func() {
			a.direct.rulesBusy = false
			if uerr != nil {
				a.direct.rulesErr = a.errText(uerr)
				return
			}
			a.direct.ruleEditOpen = false
			a.toast(a.trf("Filterregel „%s“ gespeichert", "Filter rule “%s” saved", req.Name), "success")
			a.reloadDirectState(context.Background())
		})
	}()
}

func (a *App) renderRuleFolderTarget(c *ui.Context, keyPrefix, accID string, folder *string) {
	paths := a.accountFolderPathsForPicker(accID)
	customLbl := a.tr("(Pfad manuell…)", "(Custom path…)")
	opts := append([]string(nil), paths...)
	opts = append(opts, customLbl)
	sel := customLbl
	for _, p := range paths {
		if strings.EqualFold(p, strings.TrimSpace(*folder)) {
			sel = p
			break
		}
	}
	if len(paths) > 0 {
		if ui.Select(c.Key(keyPrefix+"-pick"), &sel, opts).Width(200).Changed() {
			if sel != customLbl {
				*folder = sel
			}
		}
	}
	ui.TextInput(c.Key(keyPrefix+"-in"), folder).Placeholder("INBOX/atlassian/done").Grow(1)
}

func (a *App) renderRuleEditorPanel(c *ui.Context) {
	t := c.Theme()
	d := &a.direct
	title := a.tr("Neue Filterregel erstellen", "Create New Filter Rule")
	if d.ruleEditOrigName != "" {
		title = a.trf("Filterregel bearbeiten: %s", "Edit Filter Rule: %s", d.ruleEditOrigName)
	}
	lang := a.uiLang()
	fieldLabels, fieldKeyToLbl, fieldLblToKey := optionLabels(ruleFields, lang)
	opLabels, opKeyToLbl, opLblToKey := optionLabels(ruleOperators, lang)
	accID := d.ruleEditAccountMap[d.ruleEditAccountLbl]
	if accID == "" {
		accID = d.ruleEditOrigAccID
	}

	ui.Column(c.Key("dir-rule-editor")).FillWidth().Padding(12).Radius(8).Background(t.Background).Border(1, t.Accent).Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(13).Bold().Grow(1)
			if ui.Button(c.Key("re-x"), "✕").Padding(2, 7).Clicked() {
				d.ruleEditOpen = false
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("re-name"), &d.ruleEditName).Placeholder(a.tr("Regelname…", "Rule name…")).Grow(1)
			if len(d.ruleEditAccountMap) > 1 {
				labels := make([]string, 0, len(d.ruleEditAccountMap))
				for lbl := range d.ruleEditAccountMap {
					labels = append(labels, lbl)
				}
				sort.Strings(labels)
				ui.Select(c.Key("re-acc"), &d.ruleEditAccountLbl, labels).Width(190)
			}
			ui.Checkbox(c.Key("re-en"), &d.ruleEditEnabled, a.tr("Aktiv", "Enabled"))
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tr("Verknüpfung:", "Match:")).FontSize(11.5).TextColor(t.TextMuted)
			ui.Segmented(c.Key("re-match"), &d.ruleEditMatchMode, a.tr("Alle Bedingungen (UND)", "All conditions (AND)"), a.tr("Mind. eine (ODER)", "Any condition (OR)"))
		})

		ui.Column(c.Key("re-conds")).FillWidth().Gap(5).Children(func() {
			rmIdx := -1
			for i := range d.ruleEditConds {
				cd := &d.ruleEditConds[i]
				ui.Row(c.Key(fmt.Sprintf("re-c-%d", i))).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					fLbl := fieldKeyToLbl[cd.Field]
					if fLbl == "" {
						fLbl = fieldLabels[0]
					}
					if ui.Select(c.Key(fmt.Sprintf("re-cf-%d", i)), &fLbl, fieldLabels).Width(130).Changed() {
						if k, ok := fieldLblToKey[fLbl]; ok {
							cd.Field = k
						}
					}
					if cd.Field == "header" {
						ui.TextInput(c.Key(fmt.Sprintf("re-ch-%d", i)), &cd.HeaderName).Placeholder("X-Header / List-Id").Width(120)
					}
					oLbl := opKeyToLbl[cd.Op]
					if oLbl == "" {
						oLbl = opLabels[0]
					}
					if ui.Select(c.Key(fmt.Sprintf("re-co-%d", i)), &oLbl, opLabels).Width(135).Changed() {
						if k, ok := opLblToKey[oLbl]; ok {
							cd.Op = k
						}
					}
					if cd.Op != "isEmpty" && cd.Op != "isNotEmpty" {
						ui.TextInput(c.Key(fmt.Sprintf("re-cv-%d", i)), &cd.Value).Placeholder(a.tr("Wert (z. B. atlassian.net)", "Value (e.g. atlassian.net)")).Grow(1)
					} else {
						ui.Spacer(c)
					}
					if d.ruleEditMatchMode == 1 && i > 0 {
						andLbl := a.tr("ODER", "OR")
						if cd.BooleanAnd {
							andLbl = a.tr("UND", "AND")
						}
						if ui.Button(c.Key(fmt.Sprintf("re-cb-%d", i)), andLbl).Padding(2, 6).Clicked() {
							cd.BooleanAnd = !cd.BooleanAnd
						}
					}
					if len(d.ruleEditConds) > 1 {
						if ui.Button(c.Key(fmt.Sprintf("re-cd-%d", i)), "✕").Padding(2, 6).Clicked() {
							rmIdx = i
						}
					}
				})
			}
			if rmIdx >= 0 {
				d.ruleEditConds = append(d.ruleEditConds[:rmIdx], d.ruleEditConds[rmIdx+1:]...)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c.Key("re-cadd"), a.tr("+ Bedingung", "+ Condition")).Padding(3, 8).Clicked() {
					d.ruleEditConds = append(d.ruleEditConds, triage.NativeCondition{Field: "subject", Op: "contains", Value: "", BooleanAnd: d.ruleEditMatchMode == 0})
				}
				if ui.Button(c.Key("re-test"), a.tr("Vorschau testen (geladene Mails)", "Preview on loaded emails")).Padding(3, 8).Clicked() {
					a.runRuleEditorPreview()
				}
			})
		})

		if d.ruleEditPreviewDone {
			ui.Column(c.Key("re-pv-box")).FillWidth().Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border).Gap(3).Children(func() {
				ui.Text(c, a.trf("Vorschau: %d von %d geladenen Mails getroffen", "Preview: %d of %d loaded emails matched", d.ruleEditPreviewCount, d.ruleEditPreviewTotal)).
					FontSize(11.5).Bold().TextColor(t.Accent)
				for _, hit := range d.ruleEditPreviewHits {
					ui.Text(c, "• "+hit).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
			})
		}

		ui.Divider(c)
		ui.Text(c, a.tr("Aktionen:", "Actions:")).FontSize(11.5).Bold().TextColor(t.TextMuted)
		ui.Row(c).FillWidth().Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("re-a-read"), &d.ruleEditMarkRead, a.tr("Als gelesen markieren", "Mark as read"))
			ui.Checkbox(c.Key("re-a-star"), &d.ruleEditAddStar, a.tr("Mit Stern markieren", "Add star"))
			ui.Checkbox(c.Key("re-a-junk"), &d.ruleEditJunk, a.tr("Als Spam verschieben", "Move to Spam"))
			ui.Checkbox(c.Key("re-a-del"), &d.ruleEditDelete, a.tr("Löschen", "Delete"))
			ui.Checkbox(c.Key("re-a-stop"), &d.ruleEditStop, a.tr("Weitere Regeln stoppen", "Stop further rules"))
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("re-a-move"), &d.ruleEditMove, a.tr("Verschieben nach:", "Move to folder:")).Width(150)
			if d.ruleEditMove {
				a.renderRuleFolderTarget(c, "re-mv", accID, &d.ruleEditMoveFolder)
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("re-a-copy"), &d.ruleEditCopy, a.tr("Kopieren nach:", "Copy to folder:")).Width(150)
			if d.ruleEditCopy {
				a.renderRuleFolderTarget(c, "re-cp", accID, &d.ruleEditCopyFolder)
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if ui.Button(c.Key("re-cancel"), a.tr("Abbrechen", "Cancel")).Clicked() {
				d.ruleEditOpen = false
			}
			saveBtn := ui.PrimaryButton(c.Key("re-save"), a.tr("Regel speichern", "Save Rule"))
			if d.rulesBusy {
				saveBtn.Disabled(true)
			}
			if saveBtn.Clicked() && !d.rulesBusy {
				a.saveRuleEditor(context.Background())
			}
		})
	})
}
