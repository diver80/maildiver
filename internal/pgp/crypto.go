package pgp

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// VerifyResult describes the outcome of verifying an OpenPGP signature.
type VerifyResult struct {
	Valid                 bool
	Trusted               bool
	SignerEmail           string
	SignerUserID          string
	SignerFingerprint     string
	SignerKeyID           string
	SignerAddressMismatch bool
	Error                 string
}

func splitRawMIME(raw []byte) (outerHeaders []byte, mimeEntity []byte) {
	norm := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	norm = bytes.ReplaceAll(norm, []byte("\n"), []byte("\r\n"))

	sep := []byte("\r\n\r\n")
	idx := bytes.Index(norm, sep)
	if idx < 0 {
		return nil, norm
	}
	hdrBlock := norm[:idx]
	bodyBlock := norm[idx+len(sep):]

	lines := bytes.Split(hdrBlock, []byte("\r\n"))
	var outer, inner [][]byte
	inInner := false
	for _, ln := range lines {
		if len(ln) > 0 && (ln[0] == ' ' || ln[0] == '\t') {
			if inInner {
				inner = append(inner, ln)
			} else {
				outer = append(outer, ln)
			}
			continue
		}
		low := strings.ToLower(string(ln))
		if strings.HasPrefix(low, "content-type:") ||
			strings.HasPrefix(low, "content-transfer-encoding:") ||
			strings.HasPrefix(low, "content-disposition:") ||
			strings.HasPrefix(low, "content-id:") ||
			strings.HasPrefix(low, "content-description:") {
			inInner = true
			inner = append(inner, ln)
		} else {
			inInner = false
			outer = append(outer, ln)
		}
	}
	if len(inner) == 0 {
		inner = [][]byte{[]byte("Content-Type: text/plain; charset=utf-8")}
	}
	var outBuf, entBuf bytes.Buffer
	for _, ln := range outer {
		outBuf.Write(ln)
		outBuf.WriteString("\r\n")
	}
	for _, ln := range inner {
		entBuf.Write(ln)
		entBuf.WriteString("\r\n")
	}
	entBuf.WriteString("\r\n")
	entBuf.Write(bodyBlock)
	return outBuf.Bytes(), entBuf.Bytes()
}

// SignMIME produces an RFC 3156 multipart/signed OpenPGP SHA-256 detached signature over rawMIME.
func SignMIME(rawMIME []byte, signer *openpgp.Entity) ([]byte, error) {
	if signer == nil || signer.PrivateKey == nil {
		return nil, fmt.Errorf("kein OpenPGP-Privatschlüssel zum Signieren")
	}
	outer, entity := splitRawMIME(rawMIME)
	var sigBuf bytes.Buffer
	cfg := &packet.Config{DefaultHash: crypto.SHA256}
	if err := openpgp.ArmoredDetachSignText(&sigBuf, signer, bytes.NewReader(entity), cfg); err != nil {
		return nil, fmt.Errorf("OpenPGP Signatur fehlgeschlagen: %w", err)
	}

	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	boundary := "mdv-pgp-" + hex.EncodeToString(rnd[:])

	var out bytes.Buffer
	out.Write(outer)
	fmt.Fprintf(&out, "Content-Type: multipart/signed; micalg=\"pgp-sha256\"; protocol=\"application/pgp-signature\"; boundary=%q\r\n", boundary)
	out.WriteString("\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.Write(entity)
	out.WriteString("\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.WriteString("Content-Type: application/pgp-signature; name=\"OpenPGPSignature.asc\"\r\n")
	out.WriteString("Content-Disposition: attachment; filename=\"OpenPGPSignature.asc\"\r\n\r\n")
	out.Write(sigBuf.Bytes())
	if !bytes.HasSuffix(sigBuf.Bytes(), []byte("\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}

// EncryptMIME wraps rawMIME into an RFC 3156 multipart/encrypted message (optionally signed inside).
func EncryptMIME(rawMIME []byte, recipients []*openpgp.Entity, signer *openpgp.Entity) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("keine OpenPGP-Empfängerschlüssel zum Verschlüsseln")
	}
	outer, entity := splitRawMIME(rawMIME)

	var cipherArmored bytes.Buffer
	aw, err := armor.Encode(&cipherArmored, "PGP MESSAGE", nil)
	if err != nil {
		return nil, err
	}
	cfg := &packet.Config{
		DefaultHash:   crypto.SHA256,
		DefaultCipher: packet.CipherAES256,
	}
	ptWriter, err := openpgp.Encrypt(aw, recipients, signer, &openpgp.FileHints{IsBinary: false}, cfg)
	if err != nil {
		_ = aw.Close()
		return nil, fmt.Errorf("OpenPGP Verschlüsselung: %w", err)
	}
	if _, err := ptWriter.Write(entity); err != nil {
		_ = ptWriter.Close()
		_ = aw.Close()
		return nil, fmt.Errorf("OpenPGP Klartext schreiben: %w", err)
	}
	if err := ptWriter.Close(); err != nil {
		_ = aw.Close()
		return nil, fmt.Errorf("OpenPGP Encrypt abschließen: %w", err)
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}

	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	boundary := "mdv-pgpenc-" + hex.EncodeToString(rnd[:])

	var out bytes.Buffer
	out.Write(outer)
	fmt.Fprintf(&out, "Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=%q\r\n", boundary)
	out.WriteString("\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.WriteString("Content-Type: application/pgp-encrypted\r\n")
	out.WriteString("Content-Description: PGP/MIME version identification\r\n\r\n")
	out.WriteString("Version: 1\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.WriteString("Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
	out.WriteString("Content-Description: OpenPGP encrypted message\r\n")
	out.WriteString("Content-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n")
	out.Write(cipherArmored.Bytes())
	if !bytes.HasSuffix(cipherArmored.Bytes(), []byte("\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}

func verifyResultFromEntity(ent *openpgp.Entity, valid bool, fromEmail string, errMsg string) *VerifyResult {
	vr := &VerifyResult{
		Valid: valid,
		Error: errMsg,
	}
	if ent == nil {
		return vr
	}
	primaryEm, emails, uid := entityEmails(ent)
	vr.SignerEmail = primaryEm
	vr.SignerUserID = uid
	vr.SignerFingerprint = strings.ToUpper(hex.EncodeToString(ent.PrimaryKey.Fingerprint[:]))
	vr.SignerKeyID = strings.ToUpper(ent.PrimaryKey.KeyIdShortString())
	if valid {
		vr.Trusted = true
		if fromEmail != "" {
			matched := false
			for _, em := range emails {
				if strings.EqualFold(em, fromEmail) {
					matched = true
					break
				}
			}
			if !matched && len(emails) > 0 {
				vr.SignerAddressMismatch = true
			}
		}
	}
	return vr
}

func extractMultipartSignedRawParts(raw []byte) (part1 []byte, sigAsc []byte, ok bool) {
	norm := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	norm = bytes.ReplaceAll(norm, []byte("\n"), []byte("\r\n"))

	msg, err := mail.ReadMessage(bytes.NewReader(norm))
	if err != nil {
		return nil, nil, false
	}
	ct := msg.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.EqualFold(mt, "multipart/signed") || !strings.Contains(strings.ToLower(params["protocol"]), "pgp-signature") {
		return nil, nil, false
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, nil, false
	}
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(norm, sep)
	if idx < 0 {
		return nil, nil, false
	}
	bodyBytes := norm[idx+len(sep):]
	delim := []byte("--" + boundary + "\r\n")
	nextDelim := []byte("\r\n--" + boundary)

	start1 := bytes.Index(bodyBytes, delim)
	if start1 < 0 {
		return nil, nil, false
	}
	rest := bodyBytes[start1+len(delim):]
	end1 := bytes.Index(rest, nextDelim)
	if end1 < 0 {
		return nil, nil, false
	}
	signedRaw := rest[:end1]

	// Read part 2 cleanly via multipart.Reader to handle transfer encodings (quoted-printable/base64).
	mr := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
	if _, err := mr.NextPart(); err != nil {
		return nil, nil, false
	}
	p2, err := mr.NextPart()
	if err != nil {
		return nil, nil, false
	}
	sigBytes, err := io.ReadAll(io.LimitReader(p2, 1024*1024))
	if err != nil {
		return nil, nil, false
	}
	return signedRaw, sigBytes, true
}

func findMultipartEncryptedArmor(raw []byte) ([]byte, bool) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	ct := msg.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil, false
	}
	if !strings.EqualFold(mt, "multipart/encrypted") || !strings.Contains(strings.ToLower(params["protocol"]), "pgp-encrypted") {
		return nil, false
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, false
	}
	mr := multipart.NewReader(msg.Body, boundary)
	for {
		p, perr := mr.NextPart()
		if perr != nil {
			break
		}
		b, _ := io.ReadAll(io.LimitReader(p, 50*1024*1024))
		if bytes.Contains(b, []byte("-----BEGIN PGP MESSAGE-----")) {
			return b, true
		}
	}
	return nil, false
}

// DecryptMIME decrypts PGP/MIME (multipart/encrypted) OR Inline PGP ("-----BEGIN PGP MESSAGE-----").
func DecryptMIME(raw []byte, keyring openpgp.EntityList, fromEmail string) (plaintext []byte, vr *VerifyResult, isInline bool, isEncrypted bool, err error) {
	if armored, ok := findMultipartEncryptedArmor(raw); ok {
		isEncrypted = true
		plain, vRes, derr := decryptArmoredBlock(armored, keyring, fromEmail)
		return plain, vRes, false, true, derr
	}
	if idx := bytes.Index(raw, []byte("-----BEGIN PGP MESSAGE-----")); idx >= 0 {
		isEncrypted = true
		plain, vRes, derr := decryptArmoredBlock(raw[idx:], keyring, fromEmail)
		return plain, vRes, true, true, derr
	}
	return nil, nil, false, false, nil
}

func decryptArmoredBlock(armored []byte, keyring openpgp.EntityList, fromEmail string) ([]byte, *VerifyResult, error) {
	blk, err := armor.Decode(bytes.NewReader(armored))
	if err != nil {
		return nil, nil, fmt.Errorf("PGP-Armor ungültig: %w", err)
	}
	md, err := openpgp.ReadMessage(blk.Body, keyring, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("PGP-Entschlüsselung fehlgeschlagen: %w", err)
	}
	plain, rerr := io.ReadAll(io.LimitReader(md.UnverifiedBody, 50*1024*1024))
	if rerr != nil {
		return nil, nil, fmt.Errorf("PGP-Inhalt lesen: %w", rerr)
	}
	var vr *VerifyResult
	if md.IsSigned {
		var signerEnt *openpgp.Entity
		if md.SignedBy != nil && md.SignedBy.Entity != nil {
			signerEnt = md.SignedBy.Entity
		}
		valid := md.SignedBy != nil && md.SignatureError == nil
		errMsg := ""
		if md.SignatureError != nil {
			errMsg = md.SignatureError.Error()
		} else if md.SignedBy == nil {
			errMsg = fmt.Sprintf("Absender-Key (ID %X) nicht im Schlüsselbund", md.SignedByKeyId)
		}
		vr = verifyResultFromEntity(signerEnt, valid, fromEmail, errMsg)
	}
	return plain, vr, nil
}

// VerifyMIME checks an unencrypted RFC 3156 multipart/signed OR inline clearsigned OpenPGP message.
func VerifyMIME(raw []byte, fromEmail string, keyring openpgp.EntityList) (vr *VerifyResult, verifiedPlain []byte, isSigned bool) {
	if part1, sigAsc, ok := extractMultipartSignedRawParts(raw); ok {
		ent, err := openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(part1), bytes.NewReader(sigAsc), nil)
		if err != nil {
			// Fallback: retry with normalized LF if remote sender signed without CRLF.
			lfPart := bytes.ReplaceAll(part1, []byte("\r\n"), []byte("\n"))
			if ent2, err2 := openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(lfPart), bytes.NewReader(sigAsc), nil); err2 == nil {
				return verifyResultFromEntity(ent2, true, fromEmail, ""), nil, true
			}
			return verifyResultFromEntity(nil, false, fromEmail, err.Error()), nil, true
		}
		return verifyResultFromEntity(ent, true, fromEmail, ""), nil, true
	}

	if idx := bytes.Index(raw, []byte("-----BEGIN PGP SIGNED MESSAGE-----")); idx >= 0 {
		blk, _ := clearsign.Decode(raw[idx:])
		if blk == nil {
			return &VerifyResult{Valid: false, Error: "Clearsigned-PGP-Block beschädigt"}, nil, true
		}
		ent, err := blk.VerifySignature(keyring, nil)
		if err != nil {
			return verifyResultFromEntity(nil, false, fromEmail, err.Error()), blk.Plaintext, true
		}
		return verifyResultFromEntity(ent, true, fromEmail, ""), blk.Plaintext, true
	}
	return nil, nil, false
}
