// Package slug turns arbitrary text (Latvian, Russian, English, ...) into
// URL-safe slugs and validates user handles.
package slug

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"strings"
	"unicode"
)

var translit = map[rune]string{
	// Latvian
	'ā': "a", 'č': "c", 'ē': "e", 'ģ': "g", 'ī': "i", 'ķ': "k", 'ļ': "l",
	'ņ': "n", 'ō': "o", 'ŗ': "r", 'š': "s", 'ū': "u", 'ž': "z",
	// Common European
	'ä': "a", 'á': "a", 'à': "a", 'å': "a", 'ą': "a", 'ć': "c", 'é': "e",
	'è': "e", 'ë': "e", 'ę': "e", 'í': "i", 'ł': "l", 'ń': "n", 'ó': "o",
	'ö': "o", 'õ': "o", 'ø': "o", 'ś': "s", 'ß': "ss", 'ú': "u", 'ü': "u",
	'ų': "u", 'ý': "y", 'ź': "z", 'ż': "z", 'ė': "e", 'į': "i",
	// Russian / Ukrainian / Belarusian
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "",
	'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya", 'і': "i", 'ї': "yi",
	'є': "ye", 'ґ': "g", 'ў': "u",
}

// Make converts s into a lowercase ASCII slug of at most maxLen bytes.
// It returns an empty string if nothing usable remains.
func Make(s string, maxLen int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case translit[r] != "":
			b.WriteString(translit[r])
			dash = false
		default:
			if _, silent := translit[r]; silent {
				continue
			}
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxLen {
		out = strings.TrimRight(out[:maxLen], "-")
	}
	return out
}

var handleRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28})[a-z0-9]$`)

var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "api": true, "about": true, "edit": true,
	"login": true, "logout": true, "me": true, "new": true, "null": true,
	"privacy": true, "root": true, "settings": true, "static": true,
	"support": true, "system": true, "makers": true, "moderator": true,
}

// ValidHandle reports whether h is an acceptable user handle:
// 3–30 chars of a-z, 0-9 and dashes, not starting or ending with a dash.
func ValidHandle(h string) bool {
	return handleRe.MatchString(h) && !strings.Contains(h, "--") && !reservedHandles[h]
}

// RandomHandle returns a random handle like "maker-k3x9qa" for new users.
// Handles are never derived from the email address, which must stay private.
func RandomHandle() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 6)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b[i] = alphabet[n.Int64()]
	}
	return "maker-" + string(b)
}
