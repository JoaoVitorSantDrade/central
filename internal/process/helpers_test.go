package process

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/store"
)

var ctx = context.Background()

// testEnv é um projeto num banco temporário com relógio controlável.
type testEnv struct {
	t    *testing.T
	c    *core.Core
	e    *Engine
	now  *time.Time
	path string
}

func defaultConfig() Config {
	return Config{
		Obrigatorios: []string{"titulo"},
		Sprint: SprintCfg{Escopo: 75 * time.Minute, Congelamento: 60 * time.Minute, Corte: 30 * time.Minute,
			Retro: 20 * time.Minute},
		LimiteParado: map[string]time.Duration{"concluida": 30 * time.Minute, "aprovada": 30 * time.Minute,
			"em_andamento": 3 * time.Hour},
		OrfasApos: 12 * time.Hour,
	}
}

func newEnv(t *testing.T, roles Roles, cfg Config) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.db")
	now := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	env := &testEnv{t: t, now: &now, path: path}
	env.open(roles, cfg)
	return env
}

func (env *testEnv) open(roles Roles, cfg Config) {
	env.t.Helper()
	db, err := store.Open(env.path, core.ProjectMigrations, store.Options{})
	if err != nil {
		env.t.Fatal(err)
	}
	env.t.Cleanup(func() { db.Close() })
	env.c = core.New(db, func() time.Time { return *env.now })
	env.e = NewEngine(env.c, roles, cfg)
	env.e.loc = time.UTC
}

func (env *testEnv) enter(vaga, papel string) {
	env.t.Helper()
	if _, err := env.c.Enter(ctx, vaga, papel, "/"+vaga); err != nil {
		env.t.Fatal(err)
	}
}

func (env *testEnv) advance(d time.Duration) { *env.now = env.now.Add(d) }

func (env *testEnv) inbox(vaga string) []core.Message {
	env.t.Helper()
	ms, err := env.c.Inbox(ctx, vaga)
	if err != nil {
		env.t.Fatal(err)
	}
	return ms
}

func ofType(ms []core.Message, tipo string) []core.Message {
	var out []core.Message
	for _, m := range ms {
		if m.Tipo == tipo {
			out = append(out, m)
		}
	}
	return out
}
