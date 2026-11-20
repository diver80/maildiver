package smime

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"

	"github.com/smallstep/pkcs7"
)

func init() {
	// Never use legacy DES-CBC; AES-256-CBC is supported by Apple Mail, Outlook, Thunderbird & iOS.
	pkcs7.ContentEncryptionAlgorithm = pkcs7.EncryptionAlgorithmAES256CBC
}

// VerifyResult describes the outcome of verifying an S/MIME signature.
type VerifyResult struct {
	Valid         bool
	Trusted       bool
	SignerEmail   string
	SignerSubject string
	Issuer        string
	Fingerprint   string
	TrustDetail   string
	Error         string
	SignerCert    *x509.Certificate
}

// splitRawMIME separates top-level RFC 5322 envelope/transport headers from the MIME body entity
// (Content-* headers + body). Both outputs preserve CRLF line endings.
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

	// Unfold header lines while keeping original formatting.
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

func writeWrappedBase64(w *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(enc); i += 76 {
		end := min(i+76, len(enc))
		w.WriteString(enc[i:end])
		w.WriteString("\r\n")
	}
}

// SignMIME signs rawMIME using a detached S/MIME SHA-256 signature (multipart/signed).
func SignMIME(rawMIME []byte, id *Identity) ([]byte, error) {
	if id == nil || id.Cert == nil || id.Key == nil {
		return nil, fmt.Errorf("kein gültiges S/MIME-Zertifikat zum Signieren")
	}
	outer, entity := splitRawMIME(rawMIME)
	sd, err := pkcs7.NewSignedData(entity)
	if err != nil {
		return nil, fmt.Errorf("S/MIME SignedData: %w", err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSignerChain(id.Cert, id.Key, id.Chain, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("S/MIME Signatur fehlgeschlagen: %w", err)
	}
	sd.Detach()
	sigDER, err := sd.Finish()
	if err != nil {
		return nil, fmt.Errorf("S/MIME Finish: %w", err)
	}

	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	boundary := "mdv-smime-" + hex.EncodeToString(rnd[:])

	var out bytes.Buffer
	out.Write(outer)
	fmt.Fprintf(&out, "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=%q\r\n", boundary)
	out.WriteString("\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.Write(entity)
	// Per RFC 2046 §5.1.1, the CRLF before the boundary delimiter belongs to the delimiter, not part 1.
	out.WriteString("\r\n")
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.WriteString("Content-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString("Content-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\n")
	writeWrappedBase64(&out, sigDER)
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}

// EncryptMIME encrypts rawMIME for recipients using AES-256-CBC (application/pkcs7-mime).
func EncryptMIME(rawMIME []byte, recipients []*x509.Certificate) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("keine Empfänger-Zertifikate zum Verschlüsseln vorhanden")
	}
	outer, entity := splitRawMIME(rawMIME)
	encDER, err := pkcs7.Encrypt(entity, recipients)
	if err != nil {
		return nil, fmt.Errorf("S/MIME Verschlüsselung fehlgeschlagen: %w", err)
	}
	var out bytes.Buffer
	out.Write(outer)
	out.WriteString("Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=\"smime.p7m\"\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString("Content-Disposition: attachment; filename=\"smime.p7m\"\r\n\r\n")
	writeWrappedBase64(&out, encDER)
	return out.Bytes(), nil
}

// VerifyMIME verifies an S/MIME signed email (either multipart/signed or opaque pkcs7-mime signed-data).
// Returns (nil, nil, nil) when rawMIME is not S/MIME signed.
func VerifyMIME(rawMIME []byte, roots *x509.CertPool) (*VerifyResult, []byte, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
	if err != nil {
		return nil, nil, err
	}
	ctRaw := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ctRaw)
	if err != nil {
		return nil, nil, nil
	}
	mediaType = strings.ToLower(mediaType)

	switch mediaType {
	case "multipart/signed":
		proto := strings.ToLower(params["protocol"])
		if proto != "" && !strings.Contains(proto, "pkcs7-signature") {
			return nil, nil, nil // e.g. OpenPGP multipart/signed
		}
		boundary := params["boundary"]
		if boundary == "" {
			return nil, nil, nil
		}
		signedPart, sigDER, perr := extractMultipartSignedParts(rawMIME, boundary)
		if perr != nil {
			return &VerifyResult{Valid: false, Error: perr.Error()}, nil, perr
		}
		p7, perr := pkcs7.Parse(sigDER)
		if perr != nil {
			return &VerifyResult{Valid: false, Error: perr.Error()}, nil, perr
		}
		p7.Content = signedPart
		res := verifyPKCS7(p7, roots)
		return res, signedPart, nil

	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		smimeType := strings.ToLower(params["smime-type"])
		if smimeType != "signed-data" {
			return nil, nil, nil
		}
		derBytes, derr := decodeMIMEBody(msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
		if derr != nil {
			return &VerifyResult{Valid: false, Error: derr.Error()}, nil, derr
		}
		p7, perr := pkcs7.Parse(derBytes)
		if perr != nil {
			return &VerifyResult{Valid: false, Error: perr.Error()}, nil, perr
		}
		res := verifyPKCS7(p7, roots)
		return res, p7.Content, nil
	}
	return nil, nil, nil
}

// DecryptMIME decrypts an S/MIME enveloped-data message using the first matching identity in ids.
// Returns (decryptedFullMIME, optionalVerifyResult, error). If rawMIME is not S/MIME encrypted,
// returns (nil, nil, nil).
func DecryptMIME(rawMIME []byte, ids []*Identity, roots *x509.CertPool) ([]byte, *VerifyResult, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
	if err != nil {
		return nil, nil, err
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		return nil, nil, nil
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType != "application/pkcs7-mime" && mediaType != "application/x-pkcs7-mime" {
		return nil, nil, nil
	}
	if strings.ToLower(params["smime-type"]) == "signed-data" {
		return nil, nil, nil
	}
	derBytes, err := decodeMIMEBody(msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("S/MIME Base64-Dekodierung fehlgeschlagen: %w", err)
	}
	p7, err := pkcs7.Parse(derBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("S/MIME PKCS#7 nicht lesbar: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("kein S/MIME-Zertifikat zum Entschlüsseln installiert")
	}

	var plainEntity []byte
	var lastErr error
	for _, id := range ids {
		if id == nil || id.Cert == nil || id.Key == nil {
			continue
		}
		pt, derr := p7.Decrypt(id.Cert, id.Key)
		if derr == nil {
			plainEntity = pt
			break
		}
		lastErr = derr
	}
	if plainEntity == nil {
		if lastErr == nil {
			lastErr = fmt.Errorf("kein passender privater S/MIME-Schlüssel gefunden")
		}
		return nil, nil, fmt.Errorf("S/MIME Entschlüsselung fehlgeschlagen: %w", lastErr)
	}

	outer, _ := splitRawMIME(rawMIME)
	var full bytes.Buffer
	full.Write(outer)
	full.Write(plainEntity)
	decrypted := full.Bytes()

	// If the decrypted inner MIME is S/MIME signed (sign-then-encrypt), verify it automatically!
	vr, opaqueInner, _ := VerifyMIME(decrypted, roots)
	if vr != nil && len(opaqueInner) > 0 && strings.Contains(strings.ToLower(string(decrypted[:min(len(decrypted), 1024)])), "smime-type=signed-data") {
		var unwrapped bytes.Buffer
		unwrapped.Write(outer)
		unwrapped.Write(opaqueInner)
		decrypted = unwrapped.Bytes()
	}
	return decrypted, vr, nil
}

func verifyPKCS7(p7 *pkcs7.PKCS7, roots *x509.CertPool) *VerifyResult {
	res := &VerifyResult{}
	if signer := p7.GetOnlySigner(); signer != nil {
		res.SignerCert = signer
		res.SignerSubject = strings.TrimSpace(signer.Subject.CommonName)
		if res.SignerSubject == "" {
			res.SignerSubject = signer.Subject.String()
		}
		res.Issuer = strings.TrimSpace(signer.Issuer.CommonName)
		if res.Issuer == "" {
			res.Issuer = signer.Issuer.String()
		}
		res.Fingerprint = CertFingerprint(signer)
		if ems := certEmails(signer); len(ems) > 0 {
			res.SignerEmail = ems[0]
		}
	}

	if err := p7.Verify(); err != nil {
		res.Valid = false
		res.Error = err.Error()
		return res
	}
	res.Valid = true

	pool := roots
	if pool == nil {
		pool, _ = x509.SystemCertPool()
	}
	if pool != nil {
		if err := p7.VerifyWithChain(pool); err == nil {
			res.Trusted = true
		} else {
			res.Trusted = false
			res.TrustDetail = err.Error()
		}
	}
	return res
}

func decodeMIMEBody(cte string, r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 25*1024*1024))
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(cte), "base64") {
		cleaned := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, raw)
		return base64.StdEncoding.DecodeString(string(cleaned))
	}
	return raw, nil
}

// extractMultipartSignedParts preserves the EXACT raw bytes of part 1 (without re-encoding
// headers or whitespace, which would break the detached PKCS#7 hash) and decodes part 2's DER signature.
func extractMultipartSignedParts(rawMIME []byte, boundary string) ([]byte, []byte, error) {
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(rawMIME, sep)
	var body []byte
	if idx >= 0 {
		body = rawMIME[idx+len(sep):]
	} else {
		sepLF := []byte("\n\n")
		if idxLF := bytes.Index(rawMIME, sepLF); idxLF >= 0 {
			body = rawMIME[idxLF+len(sepLF):]
		} else {
			return nil, nil, fmt.Errorf("ungültige multipart/signed Nachricht")
		}
	}

	delim := []byte("--" + boundary)
	first := bytes.Index(body, delim)
	if first < 0 {
		return nil, nil, fmt.Errorf("multipart/signed Boundary nicht gefunden")
	}
	afterFirst := body[first+len(delim):]
	if bytes.HasPrefix(afterFirst, []byte("\r\n")) {
		afterFirst = afterFirst[2:]
	} else if bytes.HasPrefix(afterFirst, []byte("\n")) {
		afterFirst = afterFirst[1:]
	}
	second := bytes.Index(afterFirst, delim)
	if second < 0 {
		return nil, nil, fmt.Errorf("zweiter Teil der S/MIME-Signatur fehlt")
	}
	part1Raw := afterFirst[:second]
	// Per RFC 2046 §5.1.1, the CRLF immediately preceding the boundary belongs to the boundary, not part 1.
	if bytes.HasSuffix(part1Raw, []byte("\r\n")) {
		part1Raw = part1Raw[:len(part1Raw)-2]
	} else if bytes.HasSuffix(part1Raw, []byte("\n")) {
		part1Raw = part1Raw[:len(part1Raw)-1]
	}

	// Parse part 2 via standard multipart.Reader to safely extract the base64 signature payload.
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	if _, err := mr.NextPart(); err != nil {
		return nil, nil, err
	}
	sigPart, err := mr.NextPart()
	if err != nil {
		return nil, nil, fmt.Errorf("Signatur-Teil nicht lesbar: %w", err)
	}
	sigDER, err := decodeMIMEBody(sigPart.Header.Get("Content-Transfer-Encoding"), sigPart)
	if err != nil {
		return nil, nil, fmt.Errorf("S/MIME-Signatur nicht dekodierbar: %w", err)
	}
	return part1Raw, sigDER, nil
}
