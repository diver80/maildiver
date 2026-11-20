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

var (
	ruleFieldChoices = []string{"from", "to", "subject", "body", "allAddresses"}
	ruleOpChoices    = []string{"contains", "doesNotContain", "is", "isNot", "beginsWith", "endsWith"}
)

func (a *App) openRuleEditor(existing *triage.NativeFilter) {
	d := &a.direct
	d.rulesErr = ""
	d.ruleEditOpen = true
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
		d.ruleEditDelete = false
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
	d.ruleEditDelete = false
	for _, ac := range existing.Actions {
		switch ac.Type {
		case "markRead":
			d.ruleEditMarkRead = true
		case "addStar":
			d.ruleEditAddStar = true
		case "moveToFolder":
			d.ruleEditMove = true
			d.ruleEditMoveFolder = ac.TargetFolderPath
		case "delete":
			d.ruleEditDelete = true
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
	var conds []triage.NativeCondition
	for _, cd := range d.ruleEditConds {
		val := strings.TrimSpace(cd.Value)
		if val == "" {
			continue
		}
		field := strings.TrimSpace(cd.Field)
		if field == "" {
			field = "from"
		}
		op := strings.TrimSpace(cd.Op)
		if op == "" {
			op = "contains"
		}
		conds = append(conds, triage.NativeCondition{
			Field:      field,
			Op:         op,
			Value:      val,
			BooleanAnd: cd.BooleanAnd,
		})
	}
	if len(conds) == 0 {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte mindestens eine Bedingung mit Wert angeben.", "Please enter at least one condition value."))
	}
	var actions []backend.FilterActionSpec
	if d.ruleEditMarkRead {
		actions = append(actions, backend.FilterActionSpec{Type: "markRead"})
	}
	if d.ruleEditAddStar {
		actions = append(actions, backend.FilterActionSpec{Type: "addStar"})
	}
	if d.ruleEditMove {
		folder := strings.TrimSpace(d.ruleEditMoveFolder)
		if folder == "" {
			return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte einen Zielordner angeben.", "Please enter a target folder."))
		}
		actions = append(actions, backend.FilterActionSpec{Type: "moveToFolder", TargetFolderPath: folder})
	}
	if d.ruleEditDelete {
		actions = append(actions, backend.FilterActionSpec{Type: "delete"})
	}
	if len(actions) == 0 {
		return backend.FilterUpsertRequest{}, fmt.Errorf("%s", a.tr("Bitte mindestens eine Aktion auswählen.", "Please select at least one action."))
	}
	mode := "create"
	if d.ruleEditOrigName != "" && strings.EqualFold(d.ruleEditOrigName, name) && d.ruleEditOrigAccID == accID {
		mode = "update"
	}
	return backend.FilterUpsertRequest{
		AccountID:  accID,
		Name:       name,
		Enabled:    d.ruleEditEnabled,
		MatchAll:   d.ruleEditMatchMode == 0,
		Conditions: conds,
		Actions:    actions,
		Mode:       mode,
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

func (a *App) renderRuleEditorPanel(c *ui.Context) {
	t := c.Theme()
	d := &a.direct
	title := a.tr("Neue Filterregel erstellen", "Create New Filter Rule")
	if d.ruleEditOrigName != "" {
		title = a.trf("Filterregel bearbeiten: %s", "Edit Filter Rule: %s", d.ruleEditOrigName)
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
					ui.Select(c.Key(fmt.Sprintf("re-cf-%d", i)), &cd.Field, ruleFieldChoices).Width(115)
					ui.Select(c.Key(fmt.Sprintf("re-co-%d", i)), &cd.Op, ruleOpChoices).Width(135)
					ui.TextInput(c.Key(fmt.Sprintf("re-cv-%d", i)), &cd.Value).Placeholder(a.tr("Wert (z. B. atlassian.net)", "Value (e.g. atlassian.net)")).Grow(1)
					if d.ruleEditMatchMode == 1 && i > 0 {
						andLbl := "ODER"
						if cd.BooleanAnd {
							andLbl = "UND"
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
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c.Key("re-cadd"), a.tr("+ Bedingung", "+ Condition")).Padding(3, 8).Clicked() {
					d.ruleEditConds = append(d.ruleEditConds, triage.NativeCondition{Field: "subject", Op: "contains", Value: "", BooleanAnd: d.ruleEditMatchMode == 0})
				}
			})
		})

		ui.Divider(c)
		ui.Text(c, a.tr("Aktionen:", "Actions:")).FontSize(11.5).Bold().TextColor(t.TextMuted)
		ui.Row(c).FillWidth().Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("re-a-read"), &d.ruleEditMarkRead, a.tr("Als gelesen markieren", "Mark as read"))
			ui.Checkbox(c.Key("re-a-star"), &d.ruleEditAddStar, a.tr("Mit Stern markieren", "Add star"))
			ui.Checkbox(c.Key("re-a-del"), &d.ruleEditDelete, a.tr("Löschen", "Delete"))
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("re-a-move"), &d.ruleEditMove, a.tr("Verschieben nach:", "Move to folder:"))
			if d.ruleEditMove {
				ui.TextInput(c.Key("re-a-fld"), &d.ruleEditMoveFolder).Placeholder("INBOX/atlassian/done").Grow(1)
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
