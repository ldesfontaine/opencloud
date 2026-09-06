package selfupdate

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// L'unité posée par le paquet est la source des délais : ces tests l'ouvrent
// pour que ses valeurs et les constantes Go ne dérivent jamais séparément.
const unitFilePath = "../../packaging/opencloud.service"

func TestUnitStartTimeout_MatchesTheUnitFile(t *testing.T) {
	fromUnit := unitDuration(t, "TimeoutStartSec")
	if fromUnit != UnitStartTimeout {
		t.Fatalf("TimeoutStartSec de %s vaut %s, UnitStartTimeout vaut %s : les deux doivent dire le même délai", unitFilePath, fromUnit, UnitStartTimeout)
	}
}

func TestRestartTimeout_OutlastsTheUnitStartTimeout(t *testing.T) {
	if restartTimeout <= UnitStartTimeout {
		t.Fatalf("restartTimeout (%s) doit dépasser UnitStartTimeout (%s), sinon self-update tue systemctl pendant que systemd attend encore", restartTimeout, UnitStartTimeout)
	}
}

// Le défaut corrigé : avec les valeurs par défaut de systemd (5 démarrages en
// 10 s) et RestartSec=5s, la limite n'est jamais atteinte et l'unité repart sans
// fin. La fenêtre doit au moins couvrir le rythme des redémarrages automatiques.
func TestUnitStartLimit_WindowCoversACrashLoop(t *testing.T) {
	burst := unitNumber(t, "StartLimitBurst")
	if burst < 1 {
		t.Fatalf("StartLimitBurst vaut %d : il faut au moins une tentative", burst)
	}

	window := unitDuration(t, "StartLimitIntervalSec")
	loop := time.Duration(burst) * unitDuration(t, "RestartSec")
	if window < loop {
		t.Fatalf("StartLimitIntervalSec vaut %s : %d tentatives à RestartSec en prennent %s, la limite ne serait jamais atteinte", window, burst, loop)
	}
}

// unitDuration lit une durée de l'unité. systemd accepte plusieurs suffixes ;
// l'unité n'écrit que des secondes, et ce test refuse le reste plutôt que de
// deviner.
func unitDuration(t *testing.T, key string) time.Duration {
	t.Helper()
	seconds := unitNumber(t, key)
	return time.Duration(seconds) * time.Second
}

func unitNumber(t *testing.T, key string) int {
	t.Helper()
	value := unitSetting(t, key)
	number, err := strconv.Atoi(strings.TrimSuffix(value, "s"))
	if err != nil {
		t.Fatalf("%s=%s : attendu un nombre de secondes", key, value)
	}
	return number
}

func unitSetting(t *testing.T, key string) string {
	t.Helper()
	file, err := os.Open(unitFilePath)
	if err != nil {
		t.Fatalf("ouvrir %s : %v", unitFilePath, err)
	}
	defer file.Close()

	found := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		name, value, isSetting := strings.Cut(line, "=")
		if !isSetting || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.TrimSpace(name) == key {
			found = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("lire %s : %v", unitFilePath, err)
	}
	if found == "" {
		t.Fatalf("%s ne déclare pas %s", unitFilePath, key)
	}
	return found
}
