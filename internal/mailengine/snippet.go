package mailengine

import (
	"encoding/json"
	"strings"
)

// The mail list's preview line. It is made from the body sync or opening a mail already
// fetched – no body is fetched just for it – and never for encrypted mails: their plaintext
// is not stored, and an automation must not decrypt.

// snippetBackfillLimit bounds how many already fetched mails get a preview line when the
// column is added, so the migration stays quick on big caches.
const snippetBackfillLimit = 5000

// listSnippet returns the preview line of m ("" without a fetched body or when encrypted).
func listSnippet(m StoredMessage) string {
	if !m.BodyFetched || isMessageEncrypted(m) {
		return ""
	}
	text := m.BodyText
	if strings.TrimSpace(text) == "" && m.BodyHTML != "" {
		text = htmlToPlainText(m.BodyHTML)
	}
	return conversationSnippet(text)
}

// backfillSnippets fills the preview line of the newest mails whose body was fetched before
// the column existed.
func (s *Store) backfillSnippets() error {
	rows, err := s.db.Query(`
		SELECT id, substr(body_text, 1, 4000), substr(body_html, 1, 40000), headers_json, attachments_json
		FROM messages WHERE body_fetched=1 AND snippet='' ORDER BY date_unix DESC LIMIT ?`, snippetBackfillLimit)
	if err != nil {
		return err
	}
	type upd struct{ id, snippet string }
	var todo []upd
	for rows.Next() {
		var m StoredMessage
		var hdrJSON, attJSON string
		if err := rows.Scan(&m.ID, &m.BodyText, &m.BodyHTML, &hdrJSON, &attJSON); err != nil {
			_ = rows.Close()
			return err
		}
		m.BodyFetched = true
		_ = json.Unmarshal([]byte(hdrJSON), &m.Headers)
		_ = json.Unmarshal([]byte(attJSON), &m.Attachments)
		if sn := listSnippet(m); sn != "" {
			todo = append(todo, upd{m.ID, sn})
		}
	}
	rerr := rows.Err()
	_ = rows.Close()
	if rerr != nil {
		return rerr
	}
	if len(todo) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE messages SET snippet=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range todo {
		if _, err := stmt.Exec(u.snippet, u.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
