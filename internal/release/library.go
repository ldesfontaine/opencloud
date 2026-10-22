package release

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

var (
	// ErrNoRelease : cette version, ou ce binaire de cette architecture, n'a
	// pas été importé.
	ErrNoRelease = errors.New("release: version not imported")
	// ErrVersionConflict : la version est déjà importée avec d'autres sommes.
	// Une version ne se réécrit jamais.
	ErrVersionConflict = errors.New("release: version already imported with other files")
	// ErrNothingToImport : le dossier ne porte aucun binaire de la version.
	ErrNothingToImport = errors.New("release: the version lists no binary")
)

const (
	// Lisible du compte qui lance le serveur et de son groupe, pas des autres.
	libraryDirMode  = 0o750
	libraryFileMode = 0o640
	// Un binaire tient en dizaines de Mio ; au-delà, ce n'est pas le nôtre.
	maxBinarySize = 256 << 20
)

// Library est l'ensemble des versions importées, sous <état>/releases/<version>/ :
// SHA256SUMS, sa signature et les binaires. Le serveur n'y télécharge rien ;
// c'est l'opérateur qui y dépose, par Import. La clé de confiance est celle du
// binaire (TrustedKey) : les tests en injectent une.
type Library struct {
	root *os.Root
	key  PublicKey
}

// NewLibrary lit et écrit sous stateDir, la racine de l'état du serveur.
func NewLibrary(stateDir *os.Root, key PublicKey) *Library {
	return &Library{root: stateDir, key: key}
}

// BinaryName est le nom, dans une version, du binaire d'une architecture.
func BinaryName(arch string) string {
	return brand.ReleaseBinaryPrefix + arch
}

func versionDir(version Version) string {
	return path.Join(brand.ReleasesDir, version.String())
}

// Import copie sous l'état la version que contient dir (SHA256SUMS, sa
// signature et chaque binaire listé), après avoir tout vérifié : la signature
// avec la clé de confiance, puis la somme de chaque binaire. Rien n'est écrit
// d'une version qui ne passe pas. Importer deux fois la même version ne change
// rien ; la même version avec d'autres sommes est refusée.
func (library *Library) Import(dir string) (Release, error) {
	source, err := os.OpenRoot(dir)
	if err != nil {
		return Release{}, fmt.Errorf("open release folder: %w", err)
	}
	defer source.Close()
	sums, err := readSmallIn(source, brand.ReleaseSumsFile)
	if err != nil {
		return Release{}, err
	}
	signature, err := readSmallIn(source, brand.ReleaseSignatureFile)
	if err != nil {
		return Release{}, err
	}
	signed, err := VerifySums(sums, signature, library.key)
	if err != nil {
		return Release{}, err
	}
	binaries, err := readBinaries(source, signed)
	if err != nil {
		return Release{}, err
	}
	if err := library.refuseOtherSums(signed.Version, sums); err != nil {
		return Release{}, err
	}
	if err := library.store(signed, sums, signature, binaries); err != nil {
		return Release{}, err
	}
	return signed, nil
}

// BinaryNames rend, dans l'ordre, les binaires que la version liste.
func (release Release) BinaryNames() []string {
	var names []string
	for name := range release.Sums {
		if strings.HasPrefix(name, brand.ReleaseBinaryPrefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// readBinaries lit chaque binaire listé et vérifie sa somme ; tous sont
// attendus dans le dossier.
func readBinaries(source *os.Root, signed Release) (map[string][]byte, error) {
	names := signed.BinaryNames()
	if len(names) == 0 {
		return nil, ErrNothingToImport
	}
	binaries := make(map[string][]byte, len(names))
	for _, name := range names {
		file, err := source.Open(name)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		content, err := io.ReadAll(io.LimitReader(file, maxBinarySize+1))
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if len(content) > maxBinarySize {
			return nil, fmt.Errorf("%w: %s is too long", ErrMalformed, name)
		}
		if err := signed.Check(name, bytes.NewReader(content)); err != nil {
			return nil, err
		}
		binaries[name] = content
	}
	return binaries, nil
}

// refuseOtherSums : une version déjà là avec d'autres sommes ne se remplace pas.
func (library *Library) refuseOtherSums(version Version, sums []byte) error {
	existing, err := library.root.ReadFile(path.Join(versionDir(version), brand.ReleaseSumsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read imported sums: %w", err)
	}
	if !bytes.Equal(existing, sums) {
		return fmt.Errorf("%w: %s", ErrVersionConflict, version)
	}
	return nil
}

// store écrit chaque fichier qui diffère de celui déjà là. Les sommes viennent
// en dernier : une coupure laisse une version sans SHA256SUMS, que la
// bibliothèque dit non importée, et que la reprise de l'import complète.
func (library *Library) store(signed Release, sums, signature []byte, binaries map[string][]byte) error {
	dir := versionDir(signed.Version)
	if err := library.root.MkdirAll(dir, libraryDirMode); err != nil {
		return fmt.Errorf("create release folder: %w", err)
	}
	for _, name := range signed.BinaryNames() {
		if err := library.write(path.Join(dir, name), binaries[name]); err != nil {
			return err
		}
	}
	if err := library.write(path.Join(dir, brand.ReleaseSignatureFile), signature); err != nil {
		return err
	}
	return library.write(path.Join(dir, brand.ReleaseSumsFile), sums)
}

func (library *Library) write(name string, content []byte) error {
	existing, err := library.root.ReadFile(name)
	if err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err := fsx.WriteAtomic(library.root, name, content, libraryFileMode); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// Artifact est le binaire d'une version importée pour une architecture, tel
// que le serveur le tend à un agent. Les chemins sont relatifs à l'état.
type Artifact struct {
	Version       Version
	Name          string
	Path          string
	SumsPath      string
	SignaturePath string
	SHA256        string
	Size          int64
}

// Artifact relit et revérifie la version à chaque appel : la signature, puis
// la somme du binaire. Un octet changé depuis l'import est une erreur, jamais
// un binaire envoyé. ErrNoRelease si la version, ou ce binaire, manque.
func (library *Library) Artifact(version Version, arch string) (Artifact, error) {
	dir := versionDir(version)
	sumsPath := path.Join(dir, brand.ReleaseSumsFile)
	signaturePath := path.Join(dir, brand.ReleaseSignatureFile)
	sums, err := library.readImported(sumsPath)
	if err != nil {
		return Artifact{}, err
	}
	signature, err := library.readImported(signaturePath)
	if err != nil {
		return Artifact{}, err
	}
	signed, err := VerifySums(sums, signature, library.key)
	if err != nil {
		return Artifact{}, err
	}
	if signed.Version != version {
		return Artifact{}, fmt.Errorf("%w: folder %s holds %s", ErrMalformed, version, signed.Version)
	}
	name := BinaryName(arch)
	digest, listed := signed.Sums[name]
	if !listed {
		return Artifact{}, fmt.Errorf("%w: %s has no %s", ErrNoRelease, version, name)
	}
	binaryPath := path.Join(dir, name)
	size, err := library.checkBinary(signed, name, binaryPath)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		Version: version, Name: name, Path: binaryPath, SumsPath: sumsPath, SignaturePath: signaturePath,
		SHA256: digest, Size: size,
	}, nil
}

func (library *Library) checkBinary(signed Release, name, binaryPath string) (int64, error) {
	file, err := library.root.Open(binaryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("%w: %s", ErrNoRelease, binaryPath)
	}
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", name, err)
	}
	if err := signed.Check(name, file); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// readImported lit un fichier de sommes ou de signature de la bibliothèque ;
// absent, c'est que la version n'est pas importée.
func (library *Library) readImported(name string) ([]byte, error) {
	content, err := readSmallIn(library.root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoRelease, name)
	}
	return content, err
}

func readSmallIn(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()
	return readSmall(file, name)
}
