package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Estados de uma vaga. Ativo, ocioso e trabalhando são declarados pelo agente;
// encerrado vem do Leave; sumido é derivado na leitura e nunca gravado.
const (
	EstadoAtivo       = "ativo"
	EstadoOcioso      = "ocioso"
	EstadoTrabalhando = "trabalhando"
	EstadoSumido      = "sumido"
	EstadoEncerrado   = "encerrado"
)

// PadraoSumidoApos é o tempo sem batimento até a vaga aparecer como sumida.
const PadraoSumidoApos = 30 * time.Minute

// Settings são os parâmetros do projeto que o núcleo aplica.
type Settings struct {
	SumidoApos time.Duration
	// Resources são os recursos com dimensões do central.toml, por nome.
	Resources map[string]ResourceSpec
}

// DefaultSettings devolve os valores padrão.
func DefaultSettings() Settings { return Settings{SumidoApos: PadraoSumidoApos} }

// SetSettings troca os parâmetros do projeto. SumidoApos zero ou negativo
// fica no padrão.
func (c *Core) SetSettings(s Settings) {
	if s.SumidoApos <= 0 {
		s.SumidoApos = PadraoSumidoApos
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings = s
}

func (c *Core) currentSettings() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

// Enter liga a vaga à pasta. Uma vaga existente mantém o cursor (sessão substituta);
// uma pasta ligada a outra vaga é liberada dela.
func (c *Core) Enter(ctx context.Context, vaga, papel, pasta string) (Agent, error) {
	a, _, err := c.EnterWithConfig(ctx, vaga, papel, pasta, "", "")
	return a, err
}

func validEnter(vaga, papel, pasta string) error {
	if vaga == "" || papel == "" || pasta == "" {
		return invalid("vaga, papel e pasta são obrigatórios")
	}
	if vaga == Todos || vaga == Sistema || vaga == Humano {
		return invalid("%s é reservado", vaga)
	}
	return nil
}

// enterTx grava a entrada da vaga dentro de tx (os argumentos já validados).
// A sessão nova começa ativa e sem status; o cursor continua.
func enterTx(ctx context.Context, tx *sql.Tx, now, vaga, papel, pasta, area string) (Agent, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE agentes SET pasta = '(liberada) ' || vaga WHERE pasta = ? AND vaga <> ?`, pasta, vaga); err != nil {
		return Agent{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agentes (vaga, papel, pasta, entrou_em, batimento, area) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (vaga) DO UPDATE SET papel = excluded.papel, pasta = excluded.pasta,
		entrou_em = excluded.entrou_em, batimento = excluded.batimento, area = excluded.area,
		estado = '`+EstadoAtivo+`', status = '', sumido_avisado = 0`, vaga, papel, pasta, now, now, area); err != nil {
		return Agent{}, err
	}
	if err := event(ctx, tx, now, vaga, "agente.entrou", "", map[string]string{"papel": papel, "pasta": pasta, "area": area}); err != nil {
		return Agent{}, err
	}
	return scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
}

// agentCols são as colunas de uma vaga, da tabela agentes com o apelido a.
// A última é o batimento mais recente das execuções vivas da vaga (para o
// sumido), numa subconsulta: o banco tem uma conexão só, e o sumido é decidido
// com as linhas ainda abertas. Toda leitura de vaga usa estas colunas, para o
// sumido ter uma regra só.
const agentCols = `a.vaga, a.papel, a.pasta, a.entrou_em, a.batimento, a.cursor, a.estado, a.status, a.area,
	(SELECT max(e.batimento) FROM execucoes e WHERE e.vaga = a.vaga AND e.fim IS NULL)`

const agentSelect = `SELECT ` + agentCols + ` FROM agentes a`

type scanner interface{ Scan(dest ...any) error }

// agentRaw guarda as colunas de agentCols que precisam de conversão.
type agentRaw struct {
	entrou, bat string
	exec        sql.NullString
}

// dest devolve os destinos de Scan das colunas de agentCols, em ordem.
func (r *agentRaw) dest(a *Agent) []any {
	return []any{&a.Vaga, &a.Papel, &a.Pasta, &r.entrou, &r.bat, &a.Cursor, &a.Estado, &a.Status, &a.Area, &r.exec}
}

// fill converte as colunas lidas para a.
func (r *agentRaw) fill(a *Agent) {
	a.EntrouEm, a.Batimento = parseTime(r.entrou), parseTime(r.bat)
	if r.exec.Valid {
		a.execBatimento = parseTime(r.exec.String)
	}
}

func scanAgent(row scanner) (Agent, error) {
	var a Agent
	var raw agentRaw
	err := row.Scan(raw.dest(&a)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	raw.fill(&a)
	return a, nil
}

// AgentByPasta acha a vaga ligada à pasta.
func (c *Core) AgentByPasta(ctx context.Context, pasta string) (Agent, error) {
	return scanAgent(c.db.QueryRowContext(ctx, agentSelect+` WHERE pasta = ?`, pasta))
}

// Touch renova o batimento da vaga. Não muda o estado gravado (encerrado continua).
func (c *Core) Touch(ctx context.Context, vaga string) error {
	res, err := c.db.ExecContext(ctx, `UPDATE agentes SET batimento = ?, sumido_avisado = 0 WHERE vaga = ?`, c.stamp(), vaga)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus grava o estado declarado e o status livre da vaga e renova o batimento.
// estado vazio mantém o atual; só ativo, ocioso e trabalhando podem ser declarados.
func (c *Core) SetStatus(ctx context.Context, vaga, estado, status string) (Agent, error) {
	switch estado {
	case "", EstadoAtivo, EstadoOcioso, EstadoTrabalhando:
	default:
		return Agent{}, invalid("estado %q (use %s, %s ou %s)", estado, EstadoAtivo, EstadoOcioso, EstadoTrabalhando)
	}
	dados := map[string]string{"estado": estado, "status": status}
	return c.updateAgent(ctx, vaga, "agente.status", func(*sql.Tx) (any, error) { return dados, nil },
		`UPDATE agentes SET estado = CASE WHEN ? = '' THEN estado ELSE ? END, status = ?, batimento = ?, sumido_avisado = 0 WHERE vaga = ?`,
		estado, estado, status)
}

// Leave encerra a vaga e solta as travas, os pedidos e as concessões dela,
// numa transação (evento agente.saiu com o que soltou); a admissão dos
// recursos afetados roda na mesma transação. Entrar de novo a reativa.
func (c *Core) Leave(ctx context.Context, vaga string) (Agent, error) {
	ativadas := 0
	a, err := c.updateAgent(ctx, vaga, "agente.saiu", func(tx *sql.Tx) (any, error) {
		travas, err := releaseLocks(ctx, tx, vaga)
		if err != nil {
			return nil, err
		}
		concessoes, n, err := c.releaseGrants(ctx, tx, vaga)
		ativadas = n
		return map[string]any{"travas": travas, "concessoes": concessoes}, err
	}, `UPDATE agentes SET estado = '`+EstadoEncerrado+`', batimento = ? WHERE vaga = ?`)
	if err == nil && ativadas > 0 {
		c.notify.Broadcast()
	}
	return a, err
}

// updateAgent roda query (cujos dois últimos parâmetros são o horário e a vaga,
// depois de args), depois efeitos (que faz o resto da mudança e devolve os dados
// do evento) e grava o evento tipo, numa transação; devolve a vaga atualizada.
func (c *Core) updateAgent(ctx context.Context, vaga, tipo string, efeitos func(*sql.Tx) (any, error), query string, args ...any) (Agent, error) {
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, append(args, now, vaga)...)
	if err != nil {
		return Agent{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Agent{}, err
	} else if n == 0 {
		return Agent{}, ErrNotFound
	}
	dados, err := efeitos(tx)
	if err != nil {
		return Agent{}, err
	}
	if err := event(ctx, tx, now, vaga, tipo, "", dados); err != nil {
		return Agent{}, err
	}
	a, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return Agent{}, err
	}
	return a, tx.Commit()
}

// Agents lista as vagas do projeto em ordem de vaga, com o estado visível
// (sumido derivado na hora).
func (c *Core) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := c.db.QueryContext(ctx, agentSelect+` ORDER BY vaga`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now, s := c.now(), c.currentSettings()
	as := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		if c.sumido(a, now, s) {
			a.Estado = EstadoSumido
		}
		as = append(as, a)
	}
	return as, rows.Err()
}

// sumido diz se a vaga deve aparecer como sumida: não encerrada, sem batimento
// há mais de SumidoApos e sem execução viva com batimento dentro desse prazo
// (spec §5). É a única regra do sumido.
func (c *Core) sumido(a Agent, now time.Time, s Settings) bool {
	if a.Estado == EstadoEncerrado {
		return false
	}
	if !a.execBatimento.IsZero() && now.Sub(a.execBatimento) <= s.SumidoApos {
		return false
	}
	return now.Sub(a.Batimento) > s.SumidoApos
}

// deriveState marca a vaga como sumida na leitura, pela regra única do sumido.
func (c *Core) deriveState(a *Agent, now time.Time) {
	if c.sumido(*a, now, c.currentSettings()) {
		a.Estado = EstadoSumido
	}
}
