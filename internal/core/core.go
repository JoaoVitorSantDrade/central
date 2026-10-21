// Package core guarda as entidades do núcleo de um projeto: agentes, mensagens e eventos.
// Não conhece domínio nenhum (spec §4, regra 3).
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("não encontrado")
	ErrInvalid  = errors.New("inválido")
	// ErrConflict é a operação recusada pelo estado atual (ex.: trava de outra vaga).
	ErrConflict = errors.New("conflito")
)

// Todos é o destinatário que alcança todas as vagas.
const Todos = "TODOS"

// Sistema é o remetente reservado das mensagens da própria Central.
const Sistema = "CENTRAL"

// Humano é o remetente reservado das mensagens que o humano manda pelo painel.
const Humano = "HUMANO"

// TimeLayout é RFC 3339 em UTC com milissegundos fixos (ordena como texto).
// Todos os horários gravados usam este formato.
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// Agent é uma vaga ocupada por uma sessão numa pasta.
type Agent struct {
	Vaga      string    `json:"vaga"`
	Papel     string    `json:"papel"`
	Pasta     string    `json:"pasta"`
	EntrouEm  time.Time `json:"entrou_em"`
	Batimento time.Time `json:"batimento"`
	Cursor    int64     `json:"cursor"`
	Estado    string    `json:"estado"`
	Status    string    `json:"status"`
	Area      string    `json:"area"`
	// Executada diz que a Central conduz os turnos da vaga (vagas_exec).
	Executada bool `json:"executada,omitempty"`
	// execBatimento é o batimento mais recente das execuções vivas da vaga
	// (zero sem nenhuma); só o sumido o usa.
	execBatimento time.Time
}

// Message é uma mensagem entre vagas, papéis ou TODOS.
type Message struct {
	Seq      int64     `json:"seq"`
	De       string    `json:"de"`
	Para     string    `json:"para"`
	Tipo     string    `json:"tipo"`
	Ref      string    `json:"ref"`
	Corpo    string    `json:"corpo"`
	CriadaEm time.Time `json:"criada_em"`
	Chave    string    `json:"chave,omitempty"`
	// RespondeA é o seq da mensagem que esta responde; fecha a pendência dela.
	RespondeA int64 `json:"responde_a,omitempty"`
	// ResponderAte é o prazo para alguém do destino responder (nil: sem pendência).
	ResponderAte *time.Time `json:"responder_ate,omitempty"`
}

// Core opera o banco de um projeto.
type Core struct {
	db     *sql.DB
	now    func() time.Time
	notify *Notifier

	mu       sync.Mutex
	settings Settings
	// avisos guarda a hora do último aviso limitado por chave (allowNotice).
	avisos map[string]time.Time
	// limpouAmostras é a hora da última limpeza das amostras velhas.
	limpouAmostras time.Time
	// hooks rodam no fim de cada Tick (é por aí que o processo entra no relógio).
	hooks []TickHook
}

// New cria um Core sobre um banco já migrado com ProjectMigrations.
func New(db *sql.DB, now func() time.Time) *Core {
	if now == nil {
		now = time.Now
	}
	return &Core{db: db, now: now, notify: NewNotifier(), settings: DefaultSettings()}
}

// Changed devolve um canal fechado na próxima mudança visível a quem espera.
func (c *Core) Changed() <-chan struct{} { return c.notify.Wait() }

// Now é o relógio do projeto (o mesmo dos eventos), em UTC.
func (c *Core) Now() time.Time { return c.now().UTC() }

func (c *Core) stamp() string { return c.now().UTC().Format(TimeLayout) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(TimeLayout, s)
	return t
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func event(ctx context.Context, tx *sql.Tx, quando, quem, tipo, ref string, dados any) error {
	b, err := json.Marshal(dados)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO eventos (quando, quem, tipo, ref, dados) VALUES (?, ?, ?, ?, ?)`,
		quando, quem, tipo, ref, string(b))
	return err
}

const msgSelect = `SELECT seq, de, para, tipo, ref, corpo, criada_em, COALESCE(chave, ''), COALESCE(responde_a, 0), responder_ate FROM mensagens`

// SendInput são os dados de uma mensagem. ResponderEm > 0 abre uma pendência
// de resposta com prazo; RespondeA > 0 fecha a pendência da mensagem com esse seq
// (só um destinatário dela pode responder).
type SendInput struct {
	De, Para, Tipo, Ref, Corpo, Chave string
	RespondeA                         int64
	ResponderEm                       time.Duration
}

// Send grava uma mensagem. Com chave repetida, devolve a mensagem original.
func (c *Core) Send(ctx context.Context, in SendInput) (Message, error) {
	if in.De == "" || in.Para == "" || in.Tipo == "" || in.Corpo == "" {
		return Message{}, invalid("de, para, tipo e corpo são obrigatórios")
	}
	if in.ResponderEm < 0 {
		return Message{}, invalid("prazo de resposta negativo")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if in.Chave != "" {
		ms, err := queryMessages(ctx, tx, msgSelect+` WHERE de = ? AND chave = ?`, in.De, in.Chave)
		if err != nil {
			return Message{}, err
		}
		if len(ms) == 1 {
			return ms[0], nil
		}
	}
	if in.RespondeA != 0 {
		orig, err := messageBySeq(ctx, tx, in.RespondeA)
		if err != nil {
			return Message{}, err
		}
		if err := canReply(ctx, tx, in.De, orig); err != nil {
			return Message{}, err
		}
	}
	m, err := c.insertMessage(ctx, tx, in)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	c.notify.Broadcast()
	return m, nil
}

// insertMessage grava a mensagem e o evento mensagem.enviada dentro de tx.
// Quem chama faz o Broadcast depois do commit.
func (c *Core) insertMessage(ctx context.Context, tx *sql.Tx, in SendInput) (Message, error) {
	now := c.now().UTC()
	criada := now.Format(TimeLayout)
	var chaveArg, respondeArg, prazoArg any
	if in.Chave != "" {
		chaveArg = in.Chave
	}
	if in.RespondeA != 0 {
		respondeArg = in.RespondeA
	}
	dados := map[string]any{"para": in.Para, "tipo": in.Tipo}
	if in.RespondeA != 0 {
		dados["responde_a"] = in.RespondeA
	}
	if in.ResponderEm > 0 {
		prazo := now.Add(in.ResponderEm).Format(TimeLayout)
		prazoArg = prazo
		dados["responder_ate"] = prazo
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO mensagens (de, para, tipo, ref, corpo, criada_em, chave, responde_a, responder_ate)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.De, in.Para, in.Tipo, in.Ref, in.Corpo, criada, chaveArg, respondeArg, prazoArg)
	if err != nil {
		return Message{}, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	dados["seq"] = seq
	if err := event(ctx, tx, criada, in.De, "mensagem.enviada", in.Ref, dados); err != nil {
		return Message{}, err
	}
	return messageBySeq(ctx, tx, seq)
}

// sendSystem grava, dentro de tx, uma mensagem de CENTRAL. Quem chama faz o
// Broadcast depois do commit.
func (c *Core) sendSystem(ctx context.Context, tx *sql.Tx, para, tipo, ref, corpo string) error {
	_, err := c.insertMessage(ctx, tx, SendInput{De: Sistema, Para: para, Tipo: tipo, Ref: ref, Corpo: corpo})
	return err
}

func messageBySeq(ctx context.Context, q querier, seq int64) (Message, error) {
	ms, err := queryMessages(ctx, q, msgSelect+` WHERE seq = ?`, seq)
	if err != nil {
		return Message{}, err
	}
	if len(ms) == 0 {
		return Message{}, fmt.Errorf("mensagem %d: %w", seq, ErrNotFound)
	}
	return ms[0], nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryMessages(ctx context.Context, q querier, query string, args ...any) ([]Message, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ms := []Message{}
	for rows.Next() {
		var m Message
		var criada string
		var prazo sql.NullString
		if err := rows.Scan(&m.Seq, &m.De, &m.Para, &m.Tipo, &m.Ref, &m.Corpo, &criada, &m.Chave, &m.RespondeA, &prazo); err != nil {
			return nil, err
		}
		m.CriadaEm = parseTime(criada)
		if prazo.Valid {
			t := parseTime(prazo.String)
			m.ResponderAte = &t
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

const forVaga = ` WHERE seq > ? AND para IN (?, ?, '` + Todos + `') AND de <> ?`

// Inbox devolve as mensagens não lidas da vaga e avança o cursor, numa transação.
func (c *Core) Inbox(ctx context.Context, vaga string) ([]Message, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return nil, err
	}
	ms, err := queryMessages(ctx, tx, msgSelect+forVaga+` ORDER BY seq`, a.Cursor, a.Vaga, a.Papel, a.Vaga)
	if err != nil {
		return nil, err
	}
	ate := a.Cursor
	if len(ms) > 0 {
		ate = ms[len(ms)-1].Seq
	}
	now := c.stamp()
	if _, err := tx.ExecContext(ctx, `UPDATE agentes SET cursor = ?, batimento = ?, sumido_avisado = 0 WHERE vaga = ?`, ate, now, vaga); err != nil {
		return nil, err
	}
	if err := event(ctx, tx, now, vaga, "caixa.lida", "", map[string]int64{"ate": ate, "lidas": int64(len(ms))}); err != nil {
		return nil, err
	}
	return ms, tx.Commit()
}

// Unread conta as mensagens não lidas da vaga, sem avançar o cursor.
func (c *Core) Unread(ctx context.Context, vaga string) (int, error) {
	_, n, err := c.FirstUnread(ctx, vaga)
	return n, err
}

// FirstUnread devolve a primeira mensagem não lida da vaga e quantas há, sem
// avançar o cursor. Sem nenhuma, devolve a mensagem vazia e 0.
func (c *Core) FirstUnread(ctx context.Context, vaga string) (Message, int, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, 0, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return Message{}, 0, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mensagens`+forVaga, a.Cursor, a.Vaga, a.Papel, a.Vaga).Scan(&n); err != nil {
		return Message{}, 0, err
	}
	if n == 0 {
		return Message{}, 0, nil
	}
	ms, err := queryMessages(ctx, tx, msgSelect+forVaga+` ORDER BY seq LIMIT 1`, a.Cursor, a.Vaga, a.Papel, a.Vaga)
	if err != nil || len(ms) == 0 {
		return Message{}, 0, err
	}
	return ms[0], n, nil
}
