package sampler

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
)

const kilobyte = 1024

// Les compteurs cumulés de la ligne « cpu » de /proc/stat, en jiffies.
type cpuCounters struct {
	active uint64
	total  uint64
	cores  int
}

// readCPU suit htop : actif = user + nice + system + irq + softirq + steal,
// total = actif + idle + iowait. guest et guest_nice sont déjà comptés dans
// user et nice. Les lignes cpuN font le nombre de cœurs.
func readCPU(path string) (cpuCounters, error) {
	content, err := readFile(path)
	if err != nil {
		return cpuCounters{}, err
	}
	var counters cpuCounters
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if fields[0] != "cpu" {
			counters.cores++
			continue
		}
		var values [8]uint64
		for i := range values {
			values[i], _ = strconv.ParseUint(fields[i+1], 10, 64)
		}
		user, nice, system, idle, iowait, irq, softirq, steal := values[0], values[1], values[2], values[3], values[4], values[5], values[6], values[7]
		counters.active = user + nice + system + irq + softirq + steal
		counters.total = counters.active + idle + iowait
		found = true
	}
	if !found {
		return cpuCounters{}, errors.New("no cpu line")
	}
	return counters, nil
}

// cpuPercent est la part active du temps écoulé entre deux lectures ; un
// compteur qui recule, après un redémarrage, donne 0 plutôt qu'un débordement.
func cpuPercent(previous, current cpuCounters) float64 {
	if current.total <= previous.total || current.active < previous.active {
		return 0
	}
	deltaTotal := current.total - previous.total
	deltaActive := current.active - previous.active
	return float64(deltaActive) / float64(deltaTotal) * 100
}

type memCounters struct {
	total     int64
	available int64
	swapTotal int64
	swapFree  int64
}

// readMeminfo lit quatre lignes de /proc/meminfo, données en kilooctets.
func readMeminfo(path string) (memCounters, error) {
	content, err := readFile(path)
	if err != nil {
		return memCounters{}, err
	}
	var counters memCounters
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		key, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		value *= kilobyte
		switch key {
		case "MemTotal":
			counters.total = value
		case "MemAvailable":
			counters.available = value
		case "SwapTotal":
			counters.swapTotal = value
		case "SwapFree":
			counters.swapFree = value
		}
	}
	if counters.total == 0 {
		return memCounters{}, errors.New("no MemTotal line")
	}
	return counters, nil
}

// readLoad prend la charge sur une minute, premier champ de /proc/loadavg.
func readLoad(path string) (float64, error) {
	content, err := readFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0, errors.New("empty loadavg")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// Les octets reçus et émis, cumulés sur toutes les interfaces sauf la
// boucle locale.
type netCounters struct {
	rx uint64
	tx uint64
}

// readNet lit /proc/net/dev : les deux lignes d'en-tête, puis « iface: »
// suivi des colonnes de réception (octets en premier) et d'émission (octets
// en neuvième).
func readNet(path string) (netCounters, error) {
	content, err := readFile(path)
	if err != nil {
		return netCounters{}, err
	}
	var counters netCounters
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if name == "lo" || len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		counters.rx += rx
		counters.tx += tx
	}
	return counters, nil
}

// netRates fait un débit des deux compteurs ; un compteur qui recule vaut 0.
func netRates(previous, current netCounters, elapsed time.Duration) (rx, tx int64) {
	if elapsed <= 0 {
		return 0, 0
	}
	seconds := elapsed.Seconds()
	if current.rx >= previous.rx {
		rx = int64(float64(current.rx-previous.rx) / seconds)
	}
	if current.tx >= previous.tx {
		tx = int64(float64(current.tx-previous.tx) / seconds)
	}
	return rx, tx
}
