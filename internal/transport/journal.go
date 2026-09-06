package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
)

const (
	// Une entrée journald tient largement là-dedans ; au-delà, elle est
	// ignorée plutôt que de faire grandir un tampon sans fin.
	maxEntryBytes = 64 << 10
	// Le texte relayé à l'interface, coupé à la même borne que côté écran.
	maxTextBytes = 4 << 10
	// Au-delà, on cesse de relayer et on attend la fin de l'unité : un script
	// bavard ne noie ni l'interface ni la base.
	maxFollowedLines = 10000
)

// Les identifiants de message de systemd, lus dans libsystemd-core : ils
// disent la fin de l'unité sans dépendre du texte, donc ni de la langue ni de
// la version.
const (
	unitProcessExitMessageID   = "98e322203f7a4ed290d09fe03c09fe15" // « Main process exited, code=…, status=… »
	unitFailureResultMessageID = "d9b373ed55a64feb8242e02dbe79a49c" // « Failed with result '…'. »
	unitSuccessMessageID       = "7ad2d189f7e94e70a38c781354912448" // « Deactivated successfully. »
)

// errJournalEnded : le flux s'est arrêté avant que systemd ne conclue. Ce
// n'est pas une issue, c'est une absence d'issue.
var errJournalEnded = errors.New("journal stream ended before the unit concluded")

// Le curseur vient de la machine : il ne rentre dans la commande distante que
// sous cette forme, revalidée ici.
var cursorPattern = regexp.MustCompile(`^[A-Za-z0-9=;:.+_-]{1,300}$`)

func (s *SSH) Follow(ctx context.Context, actionID, afterCursor string, emit func(Line)) (Outcome, error) {
	remote, err := followCommand(actionID, afterCursor)
	if err != nil {
		return Outcome{}, err
	}

	// journalctl -f ne s'arrête jamais de lui-même : c'est nous qui le tuons
	// une fois l'unité conclue.
	followCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	command := s.command(followCtx, remote)
	stderr := &boundedBuffer{limit: maxOutputBytes}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Outcome{}, fmt.Errorf("read journal stream: %w", err)
	}
	if err := command.Start(); err != nil {
		return Outcome{}, fmt.Errorf("run ssh: %w", err)
	}

	outcome, readErr := ReadJournal(stdout, actiondir.UnitName(actionID)+".service", emit)
	cancel()
	waitErr := command.Wait()

	if readErr == nil {
		return outcome, nil
	}
	if ctx.Err() != nil {
		return Outcome{}, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) && exitError.ExitCode() == sshUnreachableExitCode {
		return Outcome{}, fmt.Errorf("%w: %s", ErrUnreachable, strings.TrimSpace(stderr.String()))
	}
	return Outcome{}, readErr
}

func followCommand(actionID, afterCursor string) (string, error) {
	if !actiondir.ValidID(actionID) {
		return "", fmt.Errorf("%w: %q", ErrRefusedName, actionID)
	}

	unit := actiondir.UnitName(actionID)
	if afterCursor == "" {
		return "journalctl -o json -u " + unit + " --lines=all -f", nil
	}
	if !cursorPattern.MatchString(afterCursor) {
		return "", fmt.Errorf("%w: curseur %q", ErrRefusedName, afterCursor)
	}
	return "journalctl -o json -u " + unit + " --after-cursor=" + afterCursor + " -f", nil
}

// ReadJournal lit un flux journald au format json, relaie les lignes du script
// et rend l'issue dès que systemd a conclu sur l'unité.
func ReadJournal(stream io.Reader, unit string, emit func(Line)) (Outcome, error) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 4096), maxEntryBytes)

	outcome := Outcome{}
	emitted := 0

	for scanner.Scan() {
		var entry journalEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // une ligne illisible ne fait pas échouer le suivi
		}

		if entry.fromManager(unit) {
			if done := entry.concludes(&outcome); done {
				return outcome, nil
			}
			continue
		}
		if entry.SourceUnit != unit {
			continue
		}

		emitted++
		if emitted > maxFollowedLines {
			continue
		}
		if emit == nil {
			continue
		}
		if emitted == maxFollowedLines {
			emit(Line{At: entry.at(), Cursor: entry.Cursor,
				Text: fmt.Sprintf("… journal tronqué : plus de %d lignes", maxFollowedLines)})
			continue
		}
		emit(Line{At: entry.at(), Text: entry.text(), Cursor: entry.Cursor})
	}

	if err := scanner.Err(); err != nil {
		return Outcome{}, fmt.Errorf("read journal stream: %w", err)
	}
	return Outcome{}, errJournalEnded
}

type journalEntry struct {
	Cursor     string          `json:"__CURSOR"`
	Realtime   string          `json:"__REALTIME_TIMESTAMP"`
	Message    json.RawMessage `json:"MESSAGE"`
	MessageID  string          `json:"MESSAGE_ID"`
	SourceUnit string          `json:"_SYSTEMD_UNIT"`
	Unit       string          `json:"UNIT"`
	PID        string          `json:"_PID"`
	ExitCode   string          `json:"EXIT_CODE"`
	ExitStatus string          `json:"EXIT_STATUS"`
	UnitResult string          `json:"UNIT_RESULT"`
}

// fromManager : la ligne vient de PID 1 et parle de notre unité. Le script,
// lui, écrit depuis l'unité elle-même.
func (e journalEntry) fromManager(unit string) bool {
	return e.PID == "1" && e.Unit == unit
}

// concludes lit ce que systemd constate. Il dit true quand l'unité est finie.
func (e journalEntry) concludes(outcome *Outcome) bool {
	switch e.MessageID {
	case unitProcessExitMessageID:
		// « Main process exited » précède toujours le constat final : on en
		// garde le code, on n'en conclut pas la fin.
		if status, err := strconv.Atoi(e.ExitStatus); err == nil {
			outcome.ExitCode = status
		}
		if e.ExitCode == "killed" || e.ExitCode == "dumped" {
			outcome.Killed = true
		}
		return false
	case unitFailureResultMessageID:
		switch e.UnitResult {
		case "timeout":
			outcome.TimedOut = true
		case "signal", "core-dump", "watchdog":
			outcome.Killed = true
		}
		return true
	case unitSuccessMessageID:
		return true
	}
	return false
}

func (e journalEntry) at() time.Time {
	microseconds, err := strconv.ParseInt(e.Realtime, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(microseconds).UTC()
}

// text : MESSAGE est une chaîne, ou un tableau d'octets quand la ligne du
// script n'est pas de l'UTF-8 valide.
func (e journalEntry) text() string {
	if len(e.Message) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(e.Message, &asString); err == nil {
		return truncateText(asString)
	}

	var octets []int
	if err := json.Unmarshal(e.Message, &octets); err != nil {
		return ""
	}
	raw := make([]byte, 0, len(octets))
	for _, octet := range octets {
		if octet < 0 || octet > 255 {
			continue
		}
		raw = append(raw, byte(octet))
	}
	return truncateText(string(raw))
}

func truncateText(text string) string {
	if len(text) <= maxTextBytes {
		return text
	}
	return text[:maxTextBytes] + "… (ligne tronquée)"
}
