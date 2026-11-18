package nativeui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/diver80/omathunder/internal/bridgeclient"
)

// Folder tree
// -----------
// folders.list returns every folder of every account, flattened depth-first, with paths such
// as "Archiv/2021". The sidebar shows them as a collapsible tree, one section per account. The
// tree is rebuilt from the paths, so an order that is not depth-first, or a folder whose parent
// is missing (filtered by bridge.accountIds), still lands in a sensible place.

// folderRow is one visible line of the folder tree.
type folderRow struct {
	Folder    bridgeclient.Folder
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

func folderKey(f bridgeclient.Folder) string {
	return f.AccountID + "\x00" + cleanFolderPath(f.Path)
}

// buildFolderRows orders folders as a tree (children after their parent, otherwise in the given
// order) and leaves out the subtrees of collapsed folders.
func buildFolderRows(folders []bridgeclient.Folder, collapsed map[string]bool) []folderRow {
	present := make(map[string]bool, len(folders))
	for _, f := range folders {
		present[folderKey(f)] = true
	}
	// parentKey is the key of the nearest listed ancestor ("" for a root).
	parentKey := func(f bridgeclient.Folder) string {
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

// groupFoldersByAccount splits folders per account in the order the accounts first appear and
// builds each account's tree.
func (a *App) groupFoldersByAccount() []folderAccountGroup {
	var order []string
	byAcc := make(map[string][]bridgeclient.Folder)
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
		groups = append(groups, folderAccountGroup{AccountID: id, Name: name, Rows: buildFolderRows(byAcc[id], a.collapsedFolders)})
	}
	return groups
}

// folderIcon picks the icon of a special folder (Thunderbird type or specialUse).
func folderIcon(f bridgeclient.Folder) *ui.SVG {
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
	case "archives":
		return iconArchive
	case "junk":
		return iconShieldChk
	}
	return iconDir
}

func (a *App) toggleFolderCollapsed(key string) {
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
	for _, g := range groups {
		title, open := g.Name, a.accountSectionOpen(g.AccountID)
		if len(groups) == 1 {
			title, open = a.tr("Ordner", "Folders"), &a.secFoldersOpen
		}
		ui.SidebarSection(c, title, open, func() {
			for _, row := range g.Rows {
				a.renderFolderRow(c, row)
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
	if !row.HasKids {
		return
	}
	item.Children(func() {
		ui.Spacer(c)
		icon, tip := iconChevronDn, a.tr("Unterordner einklappen", "Collapse subfolders")
		if row.Collapsed {
			icon, tip = iconChevronRt, a.tr("Unterordner ausklappen", "Expand subfolders")
		}
		btn := ui.ButtonBase(c.Key("fold-" + row.Key)).Padding(2).Radius(4).Label(tip).Tooltip(tip)
		btn.Children(func() { ui.Icon(c, icon).Size(13, 13).TextColor(t.TextMuted) })
		if btn.Clicked() {
			a.toggleFolderCollapsed(row.Key)
		}
	})
}
