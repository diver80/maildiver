package nativeui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/mailengine"
)

// Folder tree
// -----------
// folders.list returns every folder of every account, flattened depth-first, with paths such
// as "Archiv/2021". The sidebar shows them as a collapsible tree, one section per account. The
// tree is rebuilt from the paths, so an order that is not depth-first, or a folder whose parent
// is missing (filtered by bridge.accountIds), still lands in a sensible place.

// folderRow is one visible line of the folder tree.
type folderRow struct {
	Folder    backend.Folder
	Key       string // account + path, stable across reloads (collapse state)
	Depth     int
	HasKids   bool
	Collapsed bool
}

// folderAccountGroup holds the folder rows of one account.
type folderAccountGroup struct {
	AccountID string
	Name      string
	Rows      []folderRow
}

func cleanFolderPath(p string) string { return strings.Trim(p, "/") }

func folderKey(f backend.Folder) string {
	return f.AccountID + "\x00" + cleanFolderPath(f.Path)
}

// buildFolderRows orders folders as a tree (children after their parent, otherwise in the given
// order) and leaves out the subtrees of collapsed folders.
func buildFolderRows(folders []backend.Folder, collapsed map[string]bool) []folderRow {
	present := make(map[string]bool, len(folders))
	for _, f := range folders {
		present[folderKey(f)] = true
	}
	// parentKey is the key of the nearest listed ancestor ("" for a root).
	parentKey := func(f backend.Folder) string {
		p := cleanFolderPath(f.Path)
		for {
			i := strings.LastIndex(p, "/")
			if i <= 0 {
				return ""
			}
			p = p[:i]
			if k := f.AccountID + "\x00" + p; present[k] {
				return k
			}
		}
	}
	children := make(map[string][]int)
	var roots []int
	seen := make(map[string]bool, len(folders))
	for i, f := range folders {
		k := folderKey(f)
		if seen[k] {
			continue // a duplicate path would otherwise appear twice
		}
		seen[k] = true
		if pk := parentKey(f); pk != "" {
			children[pk] = append(children[pk], i)
		} else {
			roots = append(roots, i)
		}
	}
	out := make([]folderRow, 0, len(folders))
	var visit func(i, depth int)
	visit = func(i, depth int) {
		k := folderKey(folders[i])
		kids := children[k]
		row := folderRow{Folder: folders[i], Key: k, Depth: depth, HasKids: len(kids) > 0, Collapsed: collapsed[k]}
		out = append(out, row)
		if row.Collapsed {
			return
		}
		for _, c := range kids {
			visit(c, depth+1)
		}
	}
	for _, r := range roots {
		visit(r, 0)
	}
	return out
}

func defaultCollapsedForFolder(row folderRow) bool {
	if !row.HasKids {
		return false
	}
	// Keep level 1 (e.g. INBOX) expanded so level 2 folders (projects, clients, ...) are visible,
	// collapse level 2+ subtrees (done/todo/waiting/...) by default, and collapse Archives/YYYY.
	if row.Depth >= 1 {
		return true
	}
	t := strings.ToLower(strings.TrimSpace(row.Folder.Type))
	if t == "archive" || t == "archives" {
		return true
	}
	base := strings.ToLower(cleanFolderPath(row.Folder.Path))
	return base == "archives" || base == "archiv" || base == "archive"
}

// configuredExpandedSet returns the lowercase set of folder paths (including required ancestors)
// that should start expanded for accountID, or (nil, false) when the smart default applies.
func (a *App) configuredExpandedSet(accountID string) (map[string]bool, bool) {
	if len(a.cfg.App.ExpandedFolders) == 0 {
		return nil, false
	}
	paths, ok := a.cfg.App.ExpandedFolders[accountID]
	if !ok {
		return nil, false
	}
	set := make(map[string]bool, len(paths)*2)
	for _, raw := range paths {
		p := strings.ToLower(cleanFolderPath(raw))
		if p == "" {
			continue
		}
		set[p] = true
		// Ensure every ancestor path is also expanded so the chosen folder is actually visible.
		for {
			idx := strings.LastIndexByte(p, '/')
			if idx <= 0 {
				break
			}
			p = p[:idx]
			set[p] = true
		}
	}
	return set, true
}

// ensureDefaultFolderCollapse applies startup collapse defaults once per newly seen folder key
// without overriding any folder the user has already expanded or collapsed.
func (a *App) ensureDefaultFolderCollapse(accountID string, folders []backend.Folder) {
	if len(folders) == 0 {
		return
	}
	if a.folderCollapseSeen == nil {
		a.folderCollapseSeen = make(map[string]bool, len(folders))
	}
	expandedSet, custom := a.configuredExpandedSet(accountID)
	for _, row := range buildFolderRows(folders, nil) {
		if a.folderCollapseSeen[row.Key] {
			continue
		}
		a.folderCollapseSeen[row.Key] = true
		if a.collapsedFolders != nil {
			if _, explicit := a.collapsedFolders[row.Key]; explicit {
				continue
			}
		}
		shouldCollapse := false
		if custom {
			if row.HasKids {
				norm := strings.ToLower(cleanFolderPath(row.Folder.Path))
				shouldCollapse = !expandedSet[norm]
			}
		} else {
			shouldCollapse = defaultCollapsedForFolder(row)
		}
		if shouldCollapse {
			if a.collapsedFolders == nil {
				a.collapsedFolders = make(map[string]bool)
			}
			a.collapsedFolders[row.Key] = true
		}
	}
}

// applyConfiguredFolderCollapse re-applies configured/default collapse state immediately (e.g. after SaveSettings).
func (a *App) applyConfiguredFolderCollapse() {
	a.collapsedFolders = nil
	a.folderCollapseSeen = nil
}

// expandableFoldersForAccount returns all folders of accountID that have children, in tree order.
func (a *App) expandableFoldersForAccount(accountID string) []folderRow {
	var accFolders []backend.Folder
	for _, f := range a.folders {
		if f.AccountID == accountID {
			accFolders = append(accFolders, f)
		}
	}
	var out []folderRow
	for _, row := range buildFolderRows(accFolders, nil) {
		if row.HasKids {
			out = append(out, row)
		}
	}
	return out
}

// currentExpandedFolderPaths returns the paths of all expandable folders of accountID
// that are currently expanded in the sidebar.
func (a *App) currentExpandedFolderPaths(accountID string) []string {
	var out []string
	for _, row := range a.expandableFoldersForAccount(accountID) {
		if !a.collapsedFolders[row.Key] {
			out = append(out, cleanFolderPath(row.Folder.Path))
		}
	}
	return out
}

// draggedAccount is the typed drag-and-drop payload when reordering mail accounts.
type draggedAccount string

// groupFoldersByAccount splits folders per account in the configured a.accounts order (falling
// back to first appearance in a.folders for any unmapped account) and builds each account's tree.
func (a *App) groupFoldersByAccount() []folderAccountGroup {
	byAcc := make(map[string][]backend.Folder)
	names := make(map[string]string)
	var folderOrder []string
	for _, f := range a.folders {
		if _, ok := byAcc[f.AccountID]; !ok {
			folderOrder = append(folderOrder, f.AccountID)
		}
		byAcc[f.AccountID] = append(byAcc[f.AccountID], f)
		if names[f.AccountID] == "" && f.AccountName != "" {
			names[f.AccountID] = f.AccountName
		}
	}
	order := make([]string, 0, len(folderOrder)+len(a.accounts))
	seen := make(map[string]bool, len(folderOrder)+len(a.accounts))
	for _, acc := range a.accounts {
		if len(byAcc[acc.ID]) > 0 && !seen[acc.ID] {
			seen[acc.ID] = true
			order = append(order, acc.ID)
			if names[acc.ID] == "" && acc.Name != "" {
				names[acc.ID] = acc.Name
			}
		}
	}
	for _, id := range folderOrder {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	groups := make([]folderAccountGroup, 0, len(order))
	for _, id := range order {
		name := names[id]
		if name == "" {
			name = a.accountDisplayName(id)
		}
		a.ensureDefaultFolderCollapse(id, byAcc[id])
		groups = append(groups, folderAccountGroup{AccountID: id, Name: name, Rows: buildFolderRows(byAcc[id], a.collapsedFolders)})
	}
	return groups
}

// ReorderAccounts moves account fromID directly to the position of targetID (UI thread) and
// persists the new order to config.json so sidebar, toolbar and compose pickers stay in sync.
func (a *App) ReorderAccounts(fromID, targetID string) {
	if fromID == "" || targetID == "" || fromID == targetID {
		return
	}
	// Reorder a.accounts in memory immediately for zero-latency UI update.
	fromIdx, toIdx := -1, -1
	for i, acc := range a.accounts {
		if acc.ID == fromID {
			fromIdx = i
		}
		if acc.ID == targetID {
			toIdx = i
		}
	}
	if fromIdx >= 0 && toIdx >= 0 && fromIdx != toIdx {
		item := a.accounts[fromIdx]
		without := append(a.accounts[:fromIdx], a.accounts[fromIdx+1:]...)
		reordered := make([]backend.Account, 0, len(without)+1)
		reordered = append(reordered, without[:toIdx]...)
		reordered = append(reordered, item)
		reordered = append(reordered, without[toIdx:]...)
		a.accounts = reordered
	}

	// Keep Accounts modal list in sync as well.
	dFrom, dTo := -1, -1
	for i, acc := range a.direct.accounts {
		if acc.AccountID == fromID {
			dFrom = i
		}
		if acc.AccountID == targetID {
			dTo = i
		}
	}
	if dFrom >= 0 && dTo >= 0 && dFrom != dTo {
		item := a.direct.accounts[dFrom]
		without := append(a.direct.accounts[:dFrom], a.direct.accounts[dFrom+1:]...)
		reordered := make([]mailengine.AccountSyncStatus, 0, len(without)+1)
		reordered = append(reordered, without[:dTo]...)
		reordered = append(reordered, item)
		reordered = append(reordered, without[dTo:]...)
		a.direct.accounts = reordered
	}

	orderedIDs := make([]string, 0, len(a.accounts))
	for _, acc := range a.accounts {
		orderedIDs = append(orderedIDs, acc.ID)
	}
	if len(orderedIDs) == 0 {
		for _, acc := range a.direct.accounts {
			orderedIDs = append(orderedIDs, acc.AccountID)
		}
	}
	if len(orderedIDs) == 0 {
		return
	}

	if a.opts.Direct != nil {
		dc := a.opts.Direct
		idsCopy := append([]string(nil), orderedIDs...)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := dc.ReorderAccounts(ctx, idsCopy); err != nil {
				a.emitUpdate(func() {
					a.toast(a.tr("Reihenfolge nicht gespeichert: ", "Failed to save order: ")+err.Error(), "error")
				})
			}
		}()
		return
	}

	// Non-direct / unit-test fallback via ConfigLoader + ConfigSaver.
	if a.opts.ConfigLoader != nil && a.opts.ConfigSaver != nil {
		if cfg, err := a.opts.ConfigLoader(); err == nil && len(cfg.Direct.Accounts) > 0 {
			byID := make(map[string]int, len(cfg.Direct.Accounts))
			for i, acc := range cfg.Direct.Accounts {
				byID[acc.ID] = i
			}
			next := make([]config.DirectAccount, 0, len(cfg.Direct.Accounts))
			seen := make(map[string]bool, len(orderedIDs))
			for _, id := range orderedIDs {
				if idx, ok := byID[id]; ok && !seen[id] {
					seen[id] = true
					next = append(next, cfg.Direct.Accounts[idx])
				}
			}
			for _, acc := range cfg.Direct.Accounts {
				if !seen[acc.ID] {
					next = append(next, acc)
				}
			}
			cfg.Direct.Accounts = next
			_ = a.opts.ConfigSaver(cfg)
			a.applyConfig(cfg)
		}
	}
}

// folderIcon picks the icon of a special folder (folder type or specialUse).
func folderIcon(f backend.Folder) *ui.SVG {
	kind := strings.ToLower(f.Type)
	if kind == "" && len(f.SpecialUse) > 0 {
		kind = strings.ToLower(f.SpecialUse[0])
	}
	switch kind {
	case "inbox":
		return iconFocus
	case "sent":
		return iconSend
	case "drafts":
		return iconPen
	case "trash":
		return iconTrash
	case "archive", "archives":
		return iconArchive
	case "junk":
		return iconShieldChk
	}
	return iconDir
}

func (a *App) toggleFolderCollapsed(key string) {
	if a.folderCollapseSeen == nil {
		a.folderCollapseSeen = make(map[string]bool)
	}
	a.folderCollapseSeen[key] = true
	if a.collapsedFolders == nil {
		a.collapsedFolders = make(map[string]bool)
	}
	if a.collapsedFolders[key] {
		delete(a.collapsedFolders, key)
	} else {
		a.collapsedFolders[key] = true
	}
	if accID, _, ok := strings.Cut(key, "\x00"); ok && accID != "" {
		a.persistExpandedFoldersForAccount(accID)
	}
}

func (a *App) persistExpandedFoldersForAccount(accID string) {
	paths := a.currentExpandedFolderPaths(accID)
	if a.cfg.App.ExpandedFolders == nil {
		a.cfg.App.ExpandedFolders = make(map[string][]string)
	}
	a.cfg.App.ExpandedFolders[accID] = paths
	if a.opts.ConfigSaver == nil {
		return
	}
	cfg := a.cfg
	if a.opts.ConfigLoader != nil {
		if loaded, err := a.opts.ConfigLoader(); err == nil {
			cfg = loaded
		}
	}
	if cfg.App.ExpandedFolders == nil {
		cfg.App.ExpandedFolders = make(map[string][]string)
	}
	cfg.App.ExpandedFolders[accID] = paths
	cfg = config.Normalize(cfg)
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
}

func (a *App) persistCollapsedAccounts() {
	var collapsed []string
	for id, ptr := range a.secAccountOpen {
		if ptr != nil && !*ptr {
			collapsed = append(collapsed, id)
		}
	}
	slices.Sort(collapsed)
	a.cfg.App.CollapsedAccounts = collapsed
	if a.opts.ConfigSaver == nil {
		return
	}
	cfg := a.cfg
	if a.opts.ConfigLoader != nil {
		if loaded, err := a.opts.ConfigLoader(); err == nil {
			cfg = loaded
		}
	}
	cfg.App.CollapsedAccounts = collapsed
	cfg = config.Normalize(cfg)
	_ = a.opts.ConfigSaver(cfg)
	a.cfg = cfg
}

// accountSectionOpen returns the stable open flag of an account's sidebar section.
func (a *App) accountSectionOpen(accountID string) *bool {
	if a.secAccountOpen == nil {
		a.secAccountOpen = make(map[string]*bool)
	}
	p, ok := a.secAccountOpen[accountID]
	if !ok {
		open := !slices.Contains(a.cfg.App.CollapsedAccounts, accountID)
		p = &open
		a.secAccountOpen[accountID] = p
	}
	return p
}

// renderFolderSections draws the folder tree inside the sidebar: one section per account, or a
// single "Folders" section when only one account is listed. When multiple accounts exist, each
// account header can be dragged and dropped to reorder the accounts persistently.
func (a *App) renderFolderSections(c *ui.Context) {
	groups := a.groupFoldersByAccount()
	if len(groups) == 0 {
		return
	}
	t := c.Theme()
	renderRows := func(gCopy folderAccountGroup) {
		for _, row := range gCopy.Rows {
			a.renderFolderRow(c, row)
		}
		if a.isDirectMode() && !a.readOnly {
			if a.direct.newFolderAccountID == gCopy.AccountID {
				ui.Row(c.Key("nf-row-"+gCopy.AccountID)).FillWidth().Padding(4, 8).Gap(4).AlignItems(ui.Center).Children(func() {
					inp := ui.TextInput(c.Key("nf-in-"+gCopy.AccountID), &a.direct.newFolderName).Placeholder(a.tr("Ordnername…", "Folder name…")).Grow(1)
					okBtn := ui.Button(c.Key("nf-ok-"+gCopy.AccountID), "✓").Padding(3, 6)
					if a.direct.newFolderBusy {
						okBtn.Disabled(true)
					}
					if (okBtn.Clicked() || inp.Submitted()) && !a.direct.newFolderBusy && strings.TrimSpace(a.direct.newFolderName) != "" {
						a.CreateIMAPFolder(context.Background(), gCopy.AccountID, a.direct.newFolderName)
					}
					if ui.Button(c.Key("nf-x-"+gCopy.AccountID), "✕").Padding(3, 6).Clicked() {
						a.direct.newFolderAccountID = ""
						a.direct.newFolderName = ""
					}
				})
			} else {
				btn := ui.ButtonBase(c.Key("nf-add-"+gCopy.AccountID)).FillWidth().Padding(4, 10).Radius(6).Label(a.tr("Neuer Ordner", "New Folder"))
				btn.Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconFolderPlus).Size(13, 13).TextColor(t.TextMuted)
						ui.Text(c, a.tr("Neuer Ordner…", "New Folder…")).FontSize(11.5).TextColor(t.TextMuted)
					})
				})
				if btn.Clicked() {
					a.direct.newFolderAccountID = gCopy.AccountID
					a.direct.newFolderName = ""
				}
			}
		}
	}

	if len(groups) == 1 {
		gCopy := groups[0]
		ui.SidebarSection(c, a.tr("Ordner", "Folders"), &a.secFoldersOpen, func() {
			renderRows(gCopy)
		})
		return
	}

	for _, g := range groups {
		gCopy := g
		open := a.accountSectionOpen(gCopy.AccountID)
		sec := ui.Column(c.Key("sb-acc-"+gCopy.AccountID)).FillWidth().Gap(2).Padding(6, 0, 2, 0)
		if src, ok := ui.DragOver[draggedAccount](sec); ok && string(src) != gCopy.AccountID {
			sec.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent).Background(withAlpha(t.Accent, 0.08)).Radius(6)
		}
		if src, ok := ui.Drop[draggedAccount](sec); ok && string(src) != gCopy.AccountID {
			a.ReorderAccounts(string(src), gCopy.AccountID)
		}
		sec.Children(func() {
			hdr := ui.ButtonBase(c.Key("sb-acc-hdr-"+gCopy.AccountID)).FillWidth().Padding(5, 8).Radius(6).
				Label(gCopy.Name).
				Tooltip(a.tr("Klicken zum Ein-/Ausklappen · Ziehen zum Sortieren", "Click to collapse/expand · Drag to reorder")).
				Drag(draggedAccount(gCopy.AccountID))
			if hdr.Dragging() {
				hdr.Opacity(0.45)
			}
			hdr.Children(func() {
				ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
					chev := iconChevronDn
					if !*open {
						chev = iconChevronRt
					}
					ui.Icon(c, chev).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, gCopy.Name).FontSize(11.5).Bold().TextColor(t.TextMuted).SingleLine().Grow(1)
					ui.Text(c, "⋮⋮").FontSize(11).TextColor(withAlpha(t.TextMuted, 0.55)).Shrink(0)
				})
			})
			if hdr.Clicked() {
				*open = !*open
				a.persistCollapsedAccounts()
			}
			if *open {
				renderRows(gCopy)
			}
		})
	}
}

func (a *App) renderFolderRow(c *ui.Context, row folderRow) {
	t := c.Theme()
	f := row.Folder
	if a.direct.renameFolderID == f.ID && a.isDirectMode() && !a.readOnly {
		ui.Row(c.Key("rf-row-"+f.ID)).FillWidth().Padding(4, 8, 4, 8+float32(row.Depth)*14).Gap(4).AlignItems(ui.Center).Children(func() {
			inp := ui.TextInput(c.Key("rf-in-"+f.ID), &a.direct.renameFolderName).Placeholder(a.tr("Neuer Name…", "New name…")).Grow(1)
			okBtn := ui.Button(c.Key("rf-ok-"+f.ID), "✓").Padding(3, 6)
			if a.direct.newFolderBusy {
				okBtn.Disabled(true)
			}
			if (okBtn.Clicked() || inp.Submitted()) && !a.direct.newFolderBusy && strings.TrimSpace(a.direct.renameFolderName) != "" {
				a.RenameIMAPFolder(context.Background(), f.AccountID, f.ID, a.direct.renameFolderName)
			}
			if ui.Button(c.Key("rf-x-"+f.ID), "✕").Padding(3, 6).Clicked() {
				a.direct.renameFolderID = ""
				a.direct.renameFolderAccID = ""
				a.direct.renameFolderName = ""
			}
		})
		return
	}
	label := f.Name
	if label == "" {
		label = cleanFolderPath(f.Path)
	}
	isCustom := a.isDirectMode() && !a.readOnly && isUserManageableFolder(f)
	isActive := a.navID == "folder:"+f.ID
	item := ui.SidebarItem(c, "folder:"+f.ID, folderIcon(f), label)
	item.Padding(t.Space(1.5), t.Space(2), t.Space(1.5), t.Space(2)+float32(row.Depth)*14)
	if !row.HasKids && f.UnreadCount <= 0 && !isCustom {
		return
	}
	hovered := isActive || item.Hovered()
	item.Children(func() {
		ui.Spacer(c)
		if isCustom && (hovered || a.direct.delFolderArmID == f.ID) {
			if a.direct.delFolderArmID == f.ID && time.Since(a.direct.delFolderArmAt) < 4*time.Second {
				if ui.Button(c.Key("fd-del-yes-"+f.ID), a.tr("Löschen?", "Delete?")).Padding(2, 6).TextColor(t.Danger).Clicked() {
					a.DeleteIMAPFolder(context.Background(), f.AccountID, f.ID)
				}
			} else {
				iconBtn(c.Key("fd-menu-"+f.ID), iconDots, a.tr("Ordner verwalten", "Manage folder"), ui.Color{}, false).PopoverMenuButton(func(m *ui.Menu) {
					if m.Item(a.tr("Umbenennen…", "Rename…")).Chosen() {
						a.direct.renameFolderID = f.ID
						a.direct.renameFolderAccID = f.AccountID
						a.direct.renameFolderName = label
					}
					if m.Item(a.tr("Ordner löschen", "Delete folder")).Chosen() {
						a.direct.delFolderArmID = f.ID
						a.direct.delFolderArmAt = time.Now()
					}
				})
			}
		}
		if f.UnreadCount > 0 {
			ui.Badge(c, fmt.Sprintf("%d", f.UnreadCount))
		}
		if row.HasKids {
			icon, tip := iconChevronDn, a.tr("Unterordner einklappen", "Collapse subfolders")
			if row.Collapsed {
				icon, tip = iconChevronRt, a.tr("Unterordner ausklappen", "Expand subfolders")
			}
			btn := ui.ButtonBase(c.Key("fold-" + row.Key)).Padding(2).Radius(4).Label(tip).Tooltip(tip)
			btn.Children(func() { ui.Icon(c, icon).Size(13, 13).TextColor(t.TextMuted) })
			if btn.Clicked() {
				a.toggleFolderCollapsed(row.Key)
			}
		}
	})
}

func isUserManageableFolder(f backend.Folder) bool {
	switch strings.ToLower(strings.TrimSpace(f.Type)) {
	case "inbox", "sent", "drafts", "trash", "junk", "archive":
		return false
	}
	for _, su := range f.SpecialUse {
		switch strings.ToLower(strings.TrimSpace(su)) {
		case "inbox", "sent", "drafts", "trash", "junk", "archive", "all":
			return false
		}
	}
	return !strings.EqualFold(cleanFolderPath(f.Path), "INBOX")
}
