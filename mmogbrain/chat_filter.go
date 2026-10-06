package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Chat profanity filter.
//
// How the game did it (verified in the client, 2026-10-07): the chat service
// ran every line through a moderation service (Two Hat "Community Sift") and
// sent the verdict along with the line, under data.filter.sift (parser
// 0x142A52390):
//
//	language, response (bool), risk (int), trust (int),
//	hashed           -> message +0x510: the line with offending words as '#'
//	greybox_blanked  -> +0x508, handed to the chat display
//	events[]         -> +0x4C8: {message, trust_level, old_trust_level}
//
// The client keeps BOTH texts and picks one by the player's "Chat Profanity
// Filter" setting (default on; 0x140AC8D60 reads it through 0x1404EBC80): on,
// the hashed text when it is at least two characters, otherwise the raw text.
// On the sender's own copy of a line (sender == own GUID) the client walks the
// events (0x142AA3C50): an event whose trust_level is "UNTRUSTED" shows its
// message as a "System" chat line (0x142A38620), and the one message the game
// localizes there is the warning below (Chat_ProfanityWarning); "MUTED" mutes.
//
// The moderation service is gone, so this server filters itself: a word list
// (chatFilterWords, extendable by data/chat/profanity.txt and
// DN_CHAT_PROFANITY_FILE), matched case-insensitively with common character
// substitutions. Every line carries the verdict; a sender whose line was
// filtered gets the game's own warning. Nobody is muted: the game's muting
// rules are not known. DN_CHAT_FILTER=0 sends lines without a verdict.

// chatProfanityWarning is the warning exactly as the client compares it
// (0x142A38620, including the trailing space), so it shows the localized
// Chat_ProfanityWarning instead.
const chatProfanityWarning = "This warning serves as notice that your recent chat was inappropriate. If the language used was severely inappropriate, you may be muted and notified via email. Please refrain from using inappropriate language. "

// chatFilterDefaultWords is the built-in list. A trailing '*' makes an entry
// a ROOT, matched anywhere inside a word ("fucking"); without it an entry
// matches only a whole word, so short ones do not hit innocent words ("ass"
// in "class", "hell" in "hello").
var chatFilterDefaultWords = []string{
	// English
	"fuck*", "fuk*", "fck*", "shit*", "bitch*", "cunt*", "dick", "dicks", "dickhead*",
	"cock", "cocks", "cocksucker*", "pussy", "pussies", "asshole*", "ass", "arse", "arsehole*",
	"bastard*", "whore*", "slut*", "wanker*", "twat*", "motherfuck*", "bullshit*", "jackass*",
	"dumbass*", "retard", "retards", "retarded", "nigger*", "nigga*", "faggot*", "fag", "fags",
	"kike*", "spic", "spics", "chink", "chinks", "tranny*", "kys",
	// German
	"scheiße*", "scheisse*", "arschloch*", "fotze*", "hurensohn*", "wichser*", "schlampe*",
	"missgeburt*", "spast*", "schwuchtel*",
	// Dutch
	"kanker*", "klootzak*", "kut", "kutwijf*", "tering", "teringlijer*", "godverdomme*", "hoer", "hoeren",
	"mongool*", "lul", "eikel*",
}

var (
	chatFilterOnce  sync.Once
	chatFilterRoots []string
	chatFilterWords map[string]bool
)

func loadChatFilter() {
	chatFilterWords = map[string]bool{}
	add := func(entry string) {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" || strings.HasPrefix(entry, "#") {
			return
		}
		if strings.HasSuffix(entry, "*") {
			if root := chatFilterFold(strings.TrimSuffix(entry, "*")); root != "" {
				chatFilterRoots = append(chatFilterRoots, root)
			}
			return
		}
		if w := chatFilterFold(entry); w != "" {
			chatFilterWords[w] = true
		}
	}
	for _, w := range chatFilterDefaultWords {
		add(w)
	}
	files := []string{filepath.Join(dreadconfig.DataDir(), "chat", "profanity.txt")}
	if f := os.Getenv("DN_CHAT_PROFANITY_FILE"); f != "" {
		files = append(files, f)
	}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			add(scanner.Text())
		}
		_ = f.Close()
	}
}

// chatFilterSubstitutions maps characters used in place of letters.
var chatFilterSubstitutions = map[rune]rune{
	'@': 'a', '4': 'a', '3': 'e', '1': 'i', '!': 'i', '0': 'o', '$': 's', '5': 's', '7': 't', '+': 't',
}

// chatFilterFoldRune folds one character for matching: lower case, a common
// substitution resolved; 0 for a character that separates words.
func chatFilterFoldRune(r rune) rune {
	if s, ok := chatFilterSubstitutions[r]; ok {
		return s
	}
	r = unicode.ToLower(r)
	if unicode.IsLetter(r) {
		return r
	}
	return 0
}

func chatFilterFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if f := chatFilterFoldRune(r); f != 0 {
			b.WriteRune(f)
		}
	}
	return b.String()
}

// filterChatText returns the text with every offending word replaced by '#'
// (same length, so the line keeps its shape) and how many words were hit.
func filterChatText(text string) (string, int) {
	chatFilterOnce.Do(loadChatFilter)
	runes := []rune(text)
	hits := 0
	for i := 0; i < len(runes); {
		if chatFilterFoldRune(runes[i]) == 0 {
			i++
			continue
		}
		j := i
		for j < len(runes) && chatFilterFoldRune(runes[j]) != 0 {
			j++
		}
		word := chatFilterFold(string(runes[i:j]))
		bad := chatFilterWords[word]
		for _, root := range chatFilterRoots {
			if !bad && strings.Contains(word, root) {
				bad = true
			}
		}
		if bad {
			hits++
			for k := i; k < j; k++ {
				runes[k] = '#'
			}
		}
		i = j
	}
	return string(runes), hits
}

// chatFilterVerdict is data.filter for a chat line: the sift verdict the
// client reads. warnSender adds the event that shows the sender the game's
// warning (only on the sender's own copy).
func chatFilterVerdict(text string, warnSender bool) (map[string]any, bool) {
	if os.Getenv("DN_CHAT_FILTER") == "0" {
		return nil, false
	}
	hashed, hits := filterChatText(text)
	sift := map[string]any{
		"language":        "en",
		"response":        hits == 0, // GUESS: Community Sift's "acceptable" flag; the client does not read it
		"risk":            0,
		"trust":           0,
		"hashed":          hashed,
		"greybox_blanked": false,
	}
	if hits > 0 {
		sift["risk"] = 5 // GUESS: Community Sift risk scale 0-7; the client does not read it
	}
	if hits > 0 && warnSender {
		sift["events"] = []any{map[string]any{
			"message":         chatProfanityWarning,
			"trust_level":     "UNTRUSTED",
			"old_trust_level": "TRUSTED",
		}}
	}
	return map[string]any{"sift": sift}, hits > 0
}

// withChatFilter adds the filter verdict to a chat message event.
func withChatFilter(notice map[string]any, text string, warnSender bool) map[string]any {
	verdict, _ := chatFilterVerdict(text, warnSender)
	if verdict == nil {
		return notice
	}
	out := make(map[string]any, len(notice))
	for k, v := range notice {
		out[k] = v
	}
	if data, ok := notice["data"].(map[string]any); ok {
		copied := make(map[string]any, len(data)+1)
		for k, v := range data {
			copied[k] = v
		}
		copied["filter"] = verdict
		out["data"] = copied
	}
	return out
}
