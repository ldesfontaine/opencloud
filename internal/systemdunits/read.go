package systemdunits

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Run lance une commande et rend sa sortie standard. L'agent y met exec ; un
// test, une machine fictive.
type Run func(ctx context.Context, name string, args ...string) ([]byte, error)

const (
	readTimeout = 15 * time.Second
	// Plus de sortie que cela n'est pas une liste d'unités.
	maxOutputBytes = 4 << 20
	notBootedText  = "not been booted with systemd"
)

// listArgs : les unités de service chargées, actives ou non, sans mise en
// page. --full garde les noms entiers.
var listArgs = []string{"list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager", "--full"}

// Exec lance une commande avec un environnement vide : rien de ce que l'agent
// porte en mémoire ou en variables n'arrive à systemctl, ni ne revient.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- la commande et ses arguments sont des constantes de ce paquet.
	command.Env = []string{"LC_ALL=C", "SYSTEMD_COLORS=0", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	var stdout, stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if bytes.Contains(stderr.Bytes(), []byte(notBootedText)) {
			return nil, errNotBooted
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

var errNotBooted = errors.New("systemd is not the init system")

// limitedBuffer garde au plus maxOutputBytes octets et jette le reste : une
// sortie démesurée n'enfle pas la mémoire de l'agent.
type limitedBuffer struct {
	bytes.Buffer
}

func (b *limitedBuffer) Write(chunk []byte) (int, error) {
	room := maxOutputBytes - b.Len()
	if room > 0 {
		b.Buffer.Write(chunk[:min(room, len(chunk))])
	}
	return len(chunk), nil
}

// Read relève les unités de service de la machine. Un échec n'est pas une
// erreur : le rapport dit pourquoi rien n'a été lu.
func Read(ctx context.Context, run Run) Report {
	output, err := run(ctx, "systemctl", listArgs...)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return Report{Reason: ReasonNoSystemctl, Units: []Unit{}}
	case errors.Is(err, errNotBooted):
		return Report{Reason: ReasonNotBooted, Units: []Unit{}}
	case err != nil:
		return Report{Reason: ReasonUnreadable, Units: []Unit{}}
	}
	return Parse(output)
}

// Parse lit la sortie de « systemctl list-units » : une ligne par unité,
// « UNIT LOAD ACTIVE SUB DESCRIPTION ». Seuls les quatre premiers champs sont
// lus, et chacun passe par sa liste fermée : la description, texte libre, est
// jetée sans être regardée. Les unités sont rendues dans l'ordre des noms.
func Parse(output []byte) Report {
	report := Report{Available: true, Units: []Unit{}}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		unit, ok := parseLine(scanner.Text())
		if ok {
			report.Units = append(report.Units, unit)
		}
	}
	slices.SortFunc(report.Units, func(a, b Unit) int { return strings.Compare(a.Name, b.Name) })
	report.Units = slices.CompactFunc(report.Units, func(a, b Unit) bool { return a.Name == b.Name })
	if len(report.Units) > MaxUnits {
		report.Units = report.Units[:MaxUnits]
		report.Truncated = true
	}
	return report
}

func parseLine(line string) (Unit, bool) {
	fields := strings.Fields(strings.TrimLeft(line, "●*○× \t"))
	if len(fields) < 4 || !IsUnitName(fields[0]) {
		return Unit{}, false
	}
	load := Load(fields[1])
	if load == "not-found" {
		return Unit{}, false
	}
	unit := Unit{
		Name:   fields[0],
		Load:   closedOr(load, Loads()),
		Active: closedOr(Active(fields[2]), Actives()),
		Sub:    closedOr(Sub(fields[3]), Subs()),
	}
	return unit, true
}

// closedOr rend la valeur si la liste la connaît, Other sinon : ce que
// systemd écrit hors liste ne voyage pas.
func closedOr[T ~string](value T, list []T) T {
	if slices.Contains(list, value) {
		return value
	}
	return T(Other)
}
