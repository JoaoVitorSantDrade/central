// Package config descobre o projeto (central.toml) e onde ficam os dados da máquina.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// FileName é o arquivo que marca a raiz de um projeto.
const FileName = "central.toml"

// DefaultAddr é o endereço padrão do daemon.
const DefaultAddr = "127.0.0.1:7420"

var (
	ErrNoProject = errors.New("nenhum central.toml encontrado nesta pasta nem acima dela")
	ErrExists    = errors.New("central.toml já existe")
	ErrInvalidID = errors.New("id de projeto inválido: use letras minúsculas, dígitos e hífen (até 63)")
)

// Project identifica um projeto pela pasta onde está o central.toml.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"nome"`
	Root string `json:"raiz"`
}

type fileFormat struct {
	Projeto struct {
		ID   string `toml:"id"`
		Nome string `toml:"nome"`
	} `toml:"projeto"`
}

// FindProject sobe a partir de dir até achar central.toml.
func FindProject(dir string) (Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		path := filepath.Join(d, FileName)
		data, err := os.ReadFile(path)
		if err == nil {
			var f fileFormat
			if err := toml.Unmarshal(data, &f); err != nil {
				return Project{}, fmt.Errorf("%s: %w", path, tomlError(err))
			}
			if !core.ValidProjectID(f.Projeto.ID) {
				return Project{}, fmt.Errorf("%s: %w", path, ErrInvalidID)
			}
			return Project{ID: f.Projeto.ID, Name: f.Projeto.Nome, Root: filepath.Clean(d)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Project{}, err
		}
		if filepath.Dir(d) == d {
			return Project{}, ErrNoProject
		}
	}
}

// InitProject cria central.toml e .central/ em dir.
func InitProject(dir, id, name string) (Project, error) {
	if !core.ValidProjectID(id) {
		return Project{}, ErrInvalidID
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, err
	}
	var f fileFormat
	f.Projeto.ID, f.Projeto.Nome = id, name
	data, err := toml.Marshal(f)
	if err != nil {
		return Project{}, err
	}
	fh, err := os.OpenFile(filepath.Join(abs, FileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return Project{}, ErrExists
	}
	if err != nil {
		return Project{}, err
	}
	if _, err := fh.Write(data); err != nil {
		fh.Close()
		return Project{}, err
	}
	if err := fh.Close(); err != nil {
		return Project{}, err
	}
	if err := os.MkdirAll(filepath.Join(abs, ".central"), 0o755); err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, Root: filepath.Clean(abs)}, nil
}

// DataDir devolve a pasta de dados da máquina (CENTRAL_HOME sobrescreve).
func DataDir() (string, error) {
	if d := os.Getenv("CENTRAL_HOME"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "central"), nil
		}
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "central"), nil
}

// Addr devolve o endereço do daemon (CENTRAL_ADDR sobrescreve).
func Addr() string {
	if a := os.Getenv("CENTRAL_ADDR"); a != "" {
		return a
	}
	return DefaultAddr
}
