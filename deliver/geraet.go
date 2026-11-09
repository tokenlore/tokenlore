package deliver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// MsgOtherDevice meldet Abgleich, status und Sessionstart, wenn dieser Ordner auf diesem Rechner nicht zur
// Kopplung passt. Dann geht gar nichts an den Server.
const MsgOtherDevice = "Dieser Ordner stammt von einem anderen Rechner oder einer anderen Kopplung und liefert nichts, bis du ihn neu koppelst."

// MsgNoMachine meldet status, wenn die Systemkennung des Rechners nicht lesbar ist: Geliefert wird trotzdem,
// nur prüfen lässt sich der Rechner nicht.
const MsgNoMachine = "Die Systemkennung dieses Rechners ist nicht lesbar; ob der Ordner von einem anderen Rechner stammt, lässt sich nicht prüfen."

// MsgSignKeyMissing melden Abgleich, status und Sessionstart, wenn der private Signaturschlüssel fehlt, unbrauchbar ist
// oder nicht zum gemerkten öffentlichen Teil passt. Dann geht nichts an den Server; erst das Koppeln erzeugt einen neuen.
const MsgSignKeyMissing = "Signaturschlüssel fehlt, bitte neu koppeln."

// MsgServerTooOld melden Abgleich, status und Sessionstart statt MsgSignKeyMissing, wenn der Server
// GET /api/plugin/info nicht kennt (404): Er ist älter als dieses Plugin, Neukoppeln scheitert dort. So trifft es ein
// Gerät, das ein Plugin ohne Signaturschlüssel gekoppelt hat, bis der Betreiber den Server aktualisiert.
const MsgServerTooOld = "Dein tokenlore-Server ist älter als dieses Plugin. Der Betreiber muss ihn aktualisieren, danach koppelst du einmal neu. Deine Zahlen warten so lange hier."

// MsgSignKeyTaken melden Abgleich, status und Sessionstart, wenn der Server eine Signatur keinem Schlüssel des Geräts
// zuordnen kann (400 mit reason sign_key_unknown): Meist hat ein anderes Gerät den Schlüssel beim Koppeln übernommen,
// weil der neueste Besitznachweis gewinnt. Erst erneutes Koppeln holt ihn zurück.
const MsgSignKeyTaken = "Der Server kennt den Signaturschlüssel dieses Geräts nicht mehr, meist weil ein anderes Gerät ihn beim Koppeln übernommen hat; dieses Gerät liefert nichts, bis du es neu koppelst."

// ForeignMessage nennen Abgleich, connect und status, wenn n ältere Archivzeilen nicht mitgehen, weil der Server ihren
// früheren Signaturschlüssel einem anderen Gerät zuordnet. Neue Zeilen liefert das Gerät weiter. Der Ausweg: beide
// Geräte auf der Webseite zusammenlegen, dann gehört der Schlüssel diesem; erst das nächste Koppeln setzt
// sign_keys_refused und geliefert.json zurück, und die Zeilen gehen mit.
func ForeignMessage(n int) string {
	return ForeignLead(n) + " Was davon ungeliefert ist, fehlt dort. Neue Zahlen gehen weiter. Ist das andere Gerät derselbe Rechner, lege beide auf der Webseite unter „Geräte“ zusammen und kopple danach dieses Gerät neu; dann gehen sie mit."
}

// ForeignLead ist der erste Satz von ForeignMessage; kurz genug für die Liste der Warnungen, die Texte kürzt.
func ForeignLead(n int) string {
	what := fmt.Sprintf("%d ältere Archivzeilen gehen", n)
	if n == 1 {
		what = "1 ältere Archivzeile geht"
	}
	return what + " nicht an den Server, ihr früherer Signaturschlüssel gehört dort einem anderen Gerät."
}

var errOtherDevice = errors.New(MsgOtherDevice)

// DeviceCheck ist das Ergebnis von CheckDevice.
type DeviceCheck struct {
	Mismatch  bool // Kennung oder Rechner passt nicht zur Kopplung: nichts liefern
	NoMachine bool // Systemkennung nicht lesbar: liefern, aber warnen
}

// CheckDevice prüft vor jedem Liefern, ob dieser Ordner auf diesem Rechner zur Kopplung passt. paired ist die
// Kennung, mit der gekoppelt wurde (leer: nichts zu vergleichen), machine der Hash der Systemkennung (leer:
// nicht lesbar). Eine kaputte Kennungsdatei ohne Kopie im Merkzettel zählt als fremde Kennung und sperrt. Scheitert
// schon das Lesen der Datei (etwa fehlende Rechte), entscheidet die Kopie im Merkzettel über den Rechner; der
// Abgleich meldet den Lesefehler als Warnung.
func CheckDevice(configDir string, st *state.State, paired, machine string) DeviceCheck {
	c := DeviceCheck{NoMachine: machine == ""}
	ic, err := claudecode.CheckIdentity(configDir, st.Identity, paired)
	if err != nil {
		c.Mismatch = st.Identity != nil && claudecode.MachineConflict(*st.Identity, machine)
		return c
	}
	c.Mismatch = ic.Changed || (ic.Known && claudecode.MachineConflict(ic.Current, machine))
	return c
}

// RepairSteps nennt die Schritte zum Neukoppeln. server ist die Adresse aus der gespeicherten Verbindung.
func RepairSteps(server string) string {
	page := "der Webseite deines tokenlore-Servers unter „Gerät hinzufügen“"
	if s := strings.TrimRight(strings.TrimSpace(server), "/"); s != "" {
		page = s + "/settings/add-device"
	}
	return fmt.Sprintf("So koppelst du neu: 1. Auf %s einen Code holen. 2. Den Befehl, den die Webseite anzeigt, hier in Claude Code einfügen.", page)
}
