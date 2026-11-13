package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/client"
)

// Exit-Code von connect, wenn der Server nach dem Vorgänger fragt: Es ist nichts gekoppelt, die Frage steht in der
// Ausgabe (als Text oder mit --frage-json als JSON für das Auswahlfenster in plugin/hooks/connect.ts).
const exitPredecessorQuestion = 3

// Werte von --vorgaenger und ihre Antwort im Protokoll, in der Reihenfolge der Optionen.
var predecessorAnswers = []struct{ Value, Answer string }{
	{"gleich", client.AnswerSame},
	{"neu", client.AnswerNew},
	{"spaeter", client.AnswerLater},
}

// parsePredecessorAnswer übersetzt den Wert von --vorgaenger (gleich, neu, spaeter oder später, Groß- und
// Kleinschreibung egal) in die Antwort im Protokoll.
func parsePredecessorAnswer(v string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "später" {
		s = "spaeter"
	}
	for _, a := range predecessorAnswers {
		if s == a.Value {
			return a.Answer, nil
		}
	}
	if s == "" {
		return "", errors.New("Option --vorgaenger braucht einen Wert: gleich, neu oder spaeter.")
	}
	r := []rune(client.Clean(v))
	if len(r) > 20 {
		r = append(r[:20], '…')
	}
	return "", fmt.Errorf("Ungültiger Wert für --vorgaenger: „%s“. Erlaubt sind gleich, neu oder spaeter.", string(r))
}

// connectCommand sind die Angaben, mit denen der Nutzer connect aufgerufen hat; aus ihnen entstehen die fertigen
// Befehle mit --vorgaenger. CA und Name sind leer, wenn sie nicht angegeben wurden.
type connectCommand struct {
	URL, Code, CA, Name string
}

func (c connectCommand) with(value string) string {
	parts := []string{"/tokenlore:connect", c.URL, c.Code}
	if c.CA != "" {
		parts = append(parts, "--ca", c.CA)
	}
	if c.Name != "" {
		parts = append(parts, `--name "`+c.Name+`"`)
	}
	return strings.Join(append(parts, "--vorgaenger", value), " ")
}

// promptOption ist eine Antwort im Auswahlfenster: Label wie angezeigt, Value der Wert für --vorgaenger.
type promptOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// predecessorQuestionPrompt ist die Frage nach dem Vorgänger, fertig formuliert. Das Auswahlfenster zeigt Question
// mit Header und Options; NotPaired ist die Meldung, wenn dort keine Option gewählt wird. Before ist, was connect vor
// der Frage gemeldet hat (nur im JSON); das Fenster stellt es seiner Meldung voran.
type predecessorQuestionPrompt struct {
	Before    string         `json:"before"`
	Question  string         `json:"question"`
	Header    string         `json:"header"`
	Options   []promptOption `json:"options"`
	NotPaired string         `json:"not_paired"`
	commands  []string
}

// text ist die Frage als Text für die Ausgabe von connect ohne Auswahlfenster: Frage, je Antwort Label und Befehl.
func (p predecessorQuestionPrompt) text() string {
	var b strings.Builder
	b.WriteString(p.Question + "\n\nAntworte mit einem dieser Befehle:\n")
	for i, o := range p.Options {
		fmt.Fprintf(&b, "- %s\n  %s\n", o.Label, p.commands[i])
	}
	b.WriteString("\nBis dahin ist nichts gekoppelt; " + codeStillValid + "\n")
	return b.String()
}

const codeStillValid = "der Code bleibt bis zu seinem Ablauf gültig (10 Minuten nach dem Erzeugen auf der Webseite)."

// predecessorPrompt formuliert die Frage nach dem Vorgänger kurz: woher der Ordner stammt und wann A zuletzt
// geliefert hat, bei kürzlicher Lieferung die Warnung, ein anderes Betriebssystem und ein passender
// Signaturschlüssel nur, wenn sie zutreffen. Jede Antwort sagt im Label, was danach passiert; die empfohlene steht
// zuerst. thisOS und thisArch sind die dieses Rechners, now die Uhrzeit für „vor … Stunden“.
func predecessorPrompt(q *client.PredecessorQuestionError, cmd connectCommand, thisOS, thisArch string, now time.Time) predecessorQuestionPrompt {
	a := q.Predecessor
	name := "„" + a.Name + "“"
	var b strings.Builder
	fmt.Fprintf(&b, "Dieser Ordner stammt von %s (Gerät %d)", name, a.DeviceID)
	switch {
	case a.LastSeenAt == nil:
		b.WriteString(", das nie geliefert hat.\n")
	case a.Recent:
		fmt.Fprintf(&b, ". %s hat %s geliefert und scheint noch in Gebrauch.\n", name, ago(now.Sub(*a.LastSeenAt)))
	default:
		fmt.Fprintf(&b, ", zuletzt geliefert %s.\n", ago(now.Sub(*a.LastSeenAt)))
	}
	if osLabel(a.OS, a.Arch) != osLabel(thisOS, thisArch) {
		fmt.Fprintf(&b, "%s lief auf %s, dieser Rechner läuft auf %s.\n", name, osLabel(a.OS, a.Arch), osLabel(thisOS, thisArch))
	}
	if a.SignKeyMatch {
		fmt.Fprintf(&b, "Dieser Rechner hat den Signaturschlüssel von %s.\n", name)
	}
	if a.Recent {
		fmt.Fprintf(&b, "Ist dieser Rechner trotzdem das alte Gerät %s?", name)
	} else {
		fmt.Fprintf(&b, "Ist dieser Rechner das alte Gerät %s?", name)
	}

	labels := map[string]string{
		client.AnswerSame:  "Ja: dieser Rechner wird " + name + ", alle Zahlen bleiben",
		client.AnswerNew:   "Nein: eigenes Gerät, " + name + " bleibt",
		client.AnswerLater: "Später: eigenes Gerät, auf der Webseite entscheiden",
	}
	if a.Recent {
		labels[client.AnswerSame] = "Ja: dieser Rechner wird " + name + ", der andere Rechner liefert dann nicht mehr"
		labels[client.AnswerNew] = "Nein: eigenes Gerät, " + name + " liefert weiter"
	}
	order := []string{client.AnswerSame, client.AnswerNew, client.AnswerLater}
	switch q.Recommended {
	case client.AnswerSame:
		labels[client.AnswerSame] += " (empfohlen)"
	case client.AnswerNew:
		labels[client.AnswerNew] += " (empfohlen)"
		order = []string{client.AnswerNew, client.AnswerSame, client.AnswerLater}
	}
	p := predecessorQuestionPrompt{Header: "Vorgänger", Question: b.String()}
	for _, ans := range order {
		for _, v := range predecessorAnswers {
			if v.Answer == ans {
				p.Options = append(p.Options, promptOption{Label: labels[ans], Value: v.Value})
				p.commands = append(p.commands, cmd.with(v.Value))
			}
		}
	}
	p.NotPaired = "Nicht gekoppelt, " + codeStillValid + " Zum Koppeln einen dieser Befehle eingeben:\n" + strings.Join(p.commands, "\n")
	return p
}

// json ist die Frage für das Auswahlfenster.
func (p predecessorQuestionPrompt) json() string {
	b, _ := json.Marshal(p) // nur Texte, Marshal scheitert nicht
	return string(b) + "\n"
}

// ago formuliert eine Dauer als „vor … Stunden“ oder „vor … Tagen“.
func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "vor weniger als einer Stunde"
	case d < 48*time.Hour:
		h := int(d / time.Hour)
		return fmt.Sprintf("vor %d %s", h, plural(h, "Stunde", "Stunden"))
	}
	return fmt.Sprintf("vor %d Tagen", int(d/(24*time.Hour)))
}

// osLabel nennt Betriebssystem und Architektur lesbar; unbekannte Werte bleiben, wie sie sind (bereinigt).
func osLabel(goos, arch string) string {
	name := map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}[goos]
	if name == "" {
		name = client.Clean(goos)
	}
	if name == "" {
		name = "unbekannt"
	}
	if arch = client.Clean(arch); arch != "" {
		name += ", " + arch
	}
	return name
}
