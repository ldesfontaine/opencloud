package hostconn

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// États TCP du noyau, tels que /proc/net/tcp les écrit (include/net/tcp_states.h).
const (
	stateEstablished = 0x01
	stateListen      = 0x0A
)

// Source est ce que la lecture a besoin de savoir de la machine : où est la
// table, les adresses de ses interfaces, et le nom d'un compte. Les tests y
// mettent une fausse machine.
type Source struct {
	// ProcNet est le dossier /proc/net.
	ProcNet string
	// Own sont les adresses des interfaces de la machine.
	Own []netip.Addr
	// AccountName rend le nom du compte d'un uid ; vide s'il n'en a pas.
	AccountName func(uid int) string
}

// Local lit la vraie machine.
func Local() Source {
	return Source{ProcNet: "/proc/net", Own: interfaceAddresses(), AccountName: accountName}
}

func interfaceAddresses() []netip.Addr {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var own []netip.Addr
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok {
			if parsed, ok := netip.AddrFromSlice(network.IP); ok {
				own = append(own, parsed.Unmap())
			}
		}
	}
	return own
}

// accountName ne rend que le nom d'utilisateur : le reste d'une fiche de
// compte (nom complet, dossier) ne quitte jamais cette fonction.
func accountName(uid int) string {
	found, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return ""
	}
	return found.Username
}

// Read relève la table de l'hôte.
func Read(source Source) Report {
	var rows []row
	for _, name := range []string{"tcp", "tcp6"} {
		table, err := readTable(filepath.Join(source.ProcNet, name))
		if err != nil {
			// tcp6 manque quand IPv6 est coupé ; tcp manquant, c'est la table.
			if name == "tcp" {
				return Report{Reason: ReasonUnreadable, Connections: []Connection{}}
			}
			continue
		}
		rows = append(rows, table...)
	}
	connections := summarize(rows, source)
	report := Report{Available: true, Connections: connections}
	if len(connections) > MaxConnections {
		report.Truncated = true
		report.Connections = connections[:MaxConnections]
	}
	return report
}

// row est une ligne de la table : les deux bouts, l'état, le compte du socket.
type row struct {
	local, remote netip.AddrPort
	state         int
	uid           int
}

// summarize garde ce qui joint un port d'écoute de la machine.
//
//   - Une connexion entrante (le port local écoute) d'une adresse du réseau
//     local ou d'Internet : la source est cette adresse, ou « Internet ».
//   - Une connexion de la machine à elle-même a deux lignes ; celle du client
//     (le port distant écoute) porte le compte qui se connecte. L'autre ligne,
//     celle du serveur, est ignorée : sinon la connexion serait comptée deux
//     fois, et sous le compte du serveur.
//
// Une connexion sortante vers une autre machine n'est pas gardée : elle ne
// joint aucun port de celle-ci.
func summarize(rows []row, source Source) []Connection {
	listening := map[uint16]bool{}
	for _, item := range rows {
		if item.state == stateListen {
			listening[item.local.Port()] = true
		}
	}
	own := map[netip.Addr]bool{}
	for _, address := range source.Own {
		own[address] = true
	}
	isLocal := func(address netip.Addr) bool { return IsLocal(address) || own[address] }

	found := map[Connection]bool{}
	for _, item := range rows {
		if item.state != stateEstablished {
			continue
		}
		switch {
		case listening[item.local.Port()]:
			if isLocal(item.remote.Addr()) {
				continue
			}
			found[Connection{Port: int(item.local.Port()), Origin: originOf(item.remote.Addr()), Name: nameOf(item.remote.Addr())}] = true
		case listening[item.remote.Port()] && isLocal(item.remote.Addr()):
			found[Connection{Port: int(item.remote.Port()), Origin: OriginAccount, Name: accountOf(item.uid, source)}] = true
		}
	}
	connections := make([]Connection, 0, len(found))
	for connection := range found {
		connections = append(connections, connection)
	}
	Sort(connections)
	return connections
}

func originOf(address netip.Addr) Origin {
	if IsLAN(address) {
		return OriginAddress
	}
	return OriginInternet
}

// nameOf : l'adresse d'une machine du réseau local ; rien pour Internet.
func nameOf(address netip.Addr) string {
	if IsLAN(address) {
		return address.String()
	}
	return ""
}

// accountOf rend le nom du compte, ou « uid-N » quand la machine n'en connaît
// pas (un compte supprimé, un conteneur sans /etc/passwd) ou en connaît un que
// le rapport ne saurait pas porter.
func accountOf(uid int, source Source) string {
	if source.AccountName != nil {
		if name := source.AccountName(uid); IsAccountName(name) {
			return name
		}
	}
	return fmt.Sprintf("uid-%d", uid)
}

func readTable(path string) ([]row, error) {
	file, err := os.Open(path) // #nosec G304 -- /proc/net/tcp{,6}, ou le dossier d'un test.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseTable(file)
}

// parseTable lit la table du noyau. Une ligne qu'on ne comprend pas est
// sautée : le noyau est la source, mais une colonne en plus ne doit rien casser.
func parseTable(table io.Reader) ([]row, error) {
	var rows []row
	scanner := bufio.NewScanner(table)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	scanner.Scan() // l'en-tête
	for scanner.Scan() {
		if parsed, ok := parseRow(scanner.Text()); ok {
			rows = append(rows, parsed)
		}
	}
	return rows, scanner.Err()
}

// parseRow lit « sl local rem st tx:rx tr:when retrnsmt uid … » ; seuls les
// quatre champs utiles sont gardés, jamais l'inode ni les files.
func parseRow(line string) (row, bool) {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return row{}, false
	}
	local, localOK := parseEndpoint(fields[1])
	remote, remoteOK := parseEndpoint(fields[2])
	state, stateErr := strconv.ParseUint(fields[3], 16, 8)
	uid, uidErr := strconv.Atoi(fields[7])
	if !localOK || !remoteOK || stateErr != nil || uidErr != nil {
		return row{}, false
	}
	return row{local: local, remote: remote, state: int(state), uid: uid}, true
}

// parseEndpoint lit « 0100007F:1F90 » : l'adresse en mots de 32 bits écrits
// dans l'ordre des octets de la machine (little-endian sur x86 et arm), puis
// le port en hexadécimal.
func parseEndpoint(text string) (netip.AddrPort, bool) {
	addressText, portText, found := strings.Cut(text, ":")
	if !found {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(portText, 16, 16)
	raw, decodeErr := hex.DecodeString(addressText)
	if err != nil || decodeErr != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	for start := 0; start < len(raw); start += 4 {
		raw[start], raw[start+1], raw[start+2], raw[start+3] = raw[start+3], raw[start+2], raw[start+1], raw[start]
	}
	address, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(address.Unmap(), uint16(port)), true
}
