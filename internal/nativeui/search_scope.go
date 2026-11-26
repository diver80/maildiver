package nativeui

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Where a mail search looks. It searches everywhere unless the user narrows it to the account or
// the folder on screen; the choice stays for the session.
const (
	searchEverywhere = iota
	searchAccount
	searchFolder
)

// searchFolderID is the folder on screen ("" outside a folder).
func (a *App) searchFolderID() string {
	if strings.HasPrefix(a.navID, "folder:") {
		return strings.TrimPrefix(a.navID, "folder:")
	}
	return ""
}

// searchAccountID is the account a narrowed search stays in: the folder's account, else the
// account chosen in the toolbar ("" when none).
func (a *App) searchAccountID() string {
	if fid := a.searchFolderID(); fid != "" {
		for _, f := range a.folders {
			if f.ID == fid && f.AccountID != "" {
				return f.AccountID
			}
		}
	}
	return a.selectedAccount
}

// effectiveSearchScope is the chosen scope, widened when the view offers no such folder or account.
func (a *App) effectiveSearchScope() int {
	scope := a.searchScope
	if scope == searchFolder && a.searchFolderID() == "" {
		scope = searchAccount
	}
	if scope == searchAccount && a.searchAccountID() == "" {
		scope = searchEverywhere
	}
	return scope
}

// searchScopeIDs are the account and folder a search is limited to ("" for no limit).
func (a *App) searchScopeIDs() (accountID, folderID string) {
	switch a.effectiveSearchScope() {
	case searchFolder:
		return a.searchAccountID(), a.searchFolderID()
	case searchAccount:
		return a.searchAccountID(), ""
	}
	return "", ""
}

// searchScopeName names a scope for the scope bar.
func (a *App) searchScopeName(scope int) string {
	switch scope {
	case searchFolder:
		return a.currentViewTitle()
	case searchAccount:
		return a.accountDisplayName(a.searchAccountID())
	}
	return a.tr("Überall", "Everywhere")
}

// searchScopeIn says where the search looks, for the list header ("überall", "in avono").
func (a *App) searchScopeIn() string {
	scope := a.effectiveSearchScope()
	if scope == searchEverywhere {
		return a.tr("überall", "everywhere")
	}
	return a.trf("in %s", "in %s", a.searchScopeName(scope))
}

// searchPlaceholder is the search field's hint, naming where it searches.
func (a *App) searchPlaceholder(withKey bool) string {
	var s string
	switch scope := a.effectiveSearchScope(); scope {
	case searchEverywhere:
		s = a.tr("Alle Postfächer durchsuchen", "Search all mailboxes")
	default:
		s = a.trf("In %s suchen", "Search in %s", a.searchScopeName(scope))
	}
	if withKey {
		s += "  ( / )"
	}
	return s
}

// searchActive: the list shows search results.
func (a *App) searchActive() bool {
	return strings.TrimSpace(a.searchQuery) != "" && strings.TrimSpace(a.lastSearchQuery) != ""
}

// setSearchScope narrows or widens the search and reruns it when results are on screen.
func (a *App) setSearchScope(scope int) {
	if a.effectiveSearchScope() == scope && a.searchScope == scope {
		return
	}
	a.searchScope = scope
	if a.searchActive() {
		a.lastCheckedIndex = -1
		a.LoadMessages(context.Background())
	}
}

// renderSearchScopeBar offers where to search while a search is typed: everywhere, the account
// and the folder on screen (the last two only when there are such).
func (a *App) renderSearchScopeBar(c *ui.Context, key string) {
	if strings.TrimSpace(a.searchQuery) == "" {
		return
	}
	t := c.Theme()
	scopes := []int{searchEverywhere}
	if a.searchAccountID() != "" {
		scopes = append(scopes, searchAccount)
	}
	if a.searchFolderID() != "" {
		scopes = append(scopes, searchFolder)
	}
	cur := a.effectiveSearchScope()
	ui.Row(c.Key(key)).FillWidth().Padding(6, 10).Gap(6).Wrap().AlignItems(ui.Center).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, a.tr("Suchen in:", "Search in:")).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
		for _, sc := range scopes {
			name := a.searchScopeName(sc)
			btn := ui.Button(c.Key(fmt.Sprintf("%s-%d", key, sc)), name).Padding(2, 9).MaxWidth(220)
			if sc == cur {
				btn.Background(withAlpha(t.Accent, 0.18)).Border(1, withAlpha(t.Accent, 0.6)).TextColor(t.Accent)
			}
			if btn.Clicked() {
				a.setSearchScope(sc)
			}
		}
	})
}

// accountShortLabel is a short name of an account for list tags: the local part of its address.
func (a *App) accountShortLabel(id string) string {
	name := a.accountDisplayName(id)
	if at := strings.Index(name, "@"); at > 0 {
		return name[:at]
	}
	return name
}

// serverSearchTooltip says what the server search covers in the current scope.
func (a *App) serverSearchTooltip() string {
	switch scope := a.effectiveSearchScope(); scope {
	case searchFolder:
		return a.trf("Ordner „%s“ auf dem Server durchsuchen, auch Mails, deren Text noch nicht geladen ist", "Search folder “%s” on the server, including mail whose text is not downloaded yet", a.searchScopeName(scope))
	case searchAccount:
		return a.trf("Alle Ordner von %s auf dem Server durchsuchen (ohne Papierkorb und Spam)", "Search all folders of %s on the server (without Trash and Spam)", a.searchScopeName(scope))
	}
	return a.tr("Alle Ordner aller Konten auf dem Server durchsuchen (ohne Papierkorb und Spam)", "Search all folders of all accounts on the server (without Trash and Spam)")
}
