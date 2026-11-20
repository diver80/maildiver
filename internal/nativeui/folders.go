package nativeui

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/maildiver/internal/backend"
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

// groupFoldersByAccount splits folders per account in the order the accounts first appear and
// builds each account's tree.
func (a *App) groupFoldersByAccount() []folderAccountGroup {
	var order []string
	byAcc := make(map[string][]backend.Folder)
	names := make(map[string]string)
	for _, f := range a.folders {
		if _, ok := byAcc[f.AccountID]; !ok {
			order = append(order, f.AccountID)
		}
		byAcc[f.AccountID] = append(byAcc[f.AccountID], f)
		if names[f.AccountID] == "" && f.AccountName != "" {
			names[f.AccountID] = f.AccountName
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
}

// accountSectionOpen returns the stable open flag of an account's sidebar section.
func (a *App) accountSectionOpen(accountID string) *bool {
	if a.secAccountOpen == nil {
		a.secAccountOpen = make(map[string]*bool)
	}
	p, ok := a.secAccountOpen[accountID]
	if !ok {
		open := true
		p = &open
		a.secAccountOpen[accountID] = p
	}
	return p
}

// renderFolderSections draws the folder tree inside the sidebar: one section per account, or a
// single "Folders" section when only one account is listed.
func (a *App) renderFolderSections(c *ui.Context) {
	groups := a.groupFoldersByAccount()
	if len(groups) == 0 {
		return
	}
	t := c.Theme()
	for _, g := range groups {
		gCopy := g
		title, open := gCopy.Name, a.accountSectionOpen(gCopy.AccountID)
		if len(groups) == 1 {
			title, open = a.tr("Ordner", "Folders"), &a.secFoldersOpen
		}
		ui.SidebarSection(c, title, open, func() {
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
		})
	}
}

func (a *App) renderFolderRow(c *ui.Context, row folderRow) {
	t := c.Theme()
	f := row.Folder
	label := f.Name
	if label == "" {
		label = cleanFolderPath(f.Path)
	}
	item := ui.SidebarItem(c, "folder:"+f.ID, folderIcon(f), label)
	item.Padding(t.Space(1.5), t.Space(2), t.Space(1.5), t.Space(2)+float32(row.Depth)*14)
	if !row.HasKids && f.UnreadCount <= 0 {
		return
	}
	item.Children(func() {
		ui.Spacer(c)
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
