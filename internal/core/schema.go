package core

// ProjectMigrations é o esquema do banco de um projeto, em ordem. Nunca edite
// uma migração publicada: acrescente outra.
var ProjectMigrations = []string{
	`CREATE TABLE agentes (
		vaga      TEXT PRIMARY KEY,
		papel     TEXT NOT NULL,
		pasta     TEXT NOT NULL UNIQUE,
		entrou_em TEXT NOT NULL,
		batimento TEXT NOT NULL,
		cursor    INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE mensagens (
		seq       INTEGER PRIMARY KEY AUTOINCREMENT,
		de        TEXT NOT NULL,
		para      TEXT NOT NULL,
		tipo      TEXT NOT NULL,
		ref       TEXT NOT NULL DEFAULT '',
		corpo     TEXT NOT NULL,
		criada_em TEXT NOT NULL,
		chave     TEXT UNIQUE
	);
	CREATE INDEX mensagens_para ON mensagens (para, seq);
	CREATE TABLE eventos (
		seq    INTEGER PRIMARY KEY AUTOINCREMENT,
		quando TEXT NOT NULL,
		quem   TEXT NOT NULL,
		tipo   TEXT NOT NULL,
		ref    TEXT NOT NULL DEFAULT '',
		dados  TEXT NOT NULL DEFAULT '{}'
	);`,
	// 2: a chave de idempotência vale por remetente, não pelo projeto inteiro.
	`CREATE TABLE mensagens_v2 (
		seq       INTEGER PRIMARY KEY AUTOINCREMENT,
		de        TEXT NOT NULL,
		para      TEXT NOT NULL,
		tipo      TEXT NOT NULL,
		ref       TEXT NOT NULL DEFAULT '',
		corpo     TEXT NOT NULL,
		criada_em TEXT NOT NULL,
		chave     TEXT,
		UNIQUE (de, chave)
	);
	INSERT INTO mensagens_v2 SELECT seq, de, para, tipo, ref, corpo, criada_em, chave FROM mensagens;
	DROP TABLE mensagens;
	ALTER TABLE mensagens_v2 RENAME TO mensagens;
	CREATE INDEX mensagens_para ON mensagens (para, seq);`,
	// 3: M1a. Configuração do projeto (central.toml) enviada pelo entrar; a vigente é a última linha.
	`CREATE TABLE config (
		versao INTEGER PRIMARY KEY AUTOINCREMENT,
		texto  TEXT NOT NULL,
		hash   TEXT NOT NULL,
		vaga   TEXT NOT NULL,
		quando TEXT NOT NULL
	);
	-- Estado declarado da vaga (sumido é derivado), status livre e se o aviso de sumido já saiu.
	ALTER TABLE agentes ADD estado TEXT NOT NULL DEFAULT 'ativo';
	ALTER TABLE agentes ADD status TEXT NOT NULL DEFAULT '';
	ALTER TABLE agentes ADD sumido_avisado INTEGER NOT NULL DEFAULT 0;
	-- Respostas com prazo: a pendência fecha quando chega uma mensagem com responde_a = seq.
	ALTER TABLE mensagens ADD responde_a INTEGER;
	ALTER TABLE mensagens ADD responder_ate TEXT;
	ALTER TABLE mensagens ADD prazo_avisado INTEGER NOT NULL DEFAULT 0;
	CREATE INDEX mensagens_responde_a ON mensagens (responde_a);
	-- Travas nomeadas: valem enquanto o dono não estiver sumido nem encerrado.
	CREATE TABLE travas (
		nome   TEXT PRIMARY KEY,
		vaga   TEXT NOT NULL,
		motivo TEXT NOT NULL,
		inicio TEXT NOT NULL
	);
	CREATE INDEX travas_vaga ON travas (vaga);
	-- Concessões de recursos: na fila, ativas ou encerradas. declarado e pico são JSON por dimensão.
	CREATE TABLE concessoes (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		recurso         TEXT NOT NULL,
		vaga            TEXT NOT NULL,
		tipo            TEXT NOT NULL DEFAULT '',
		prioridade      TEXT NOT NULL,
		motivo          TEXT NOT NULL DEFAULT '',
		declarado       TEXT NOT NULL DEFAULT '{}',
		exclusivo       INTEGER NOT NULL DEFAULT 0,
		duracao_ms      INTEGER NOT NULL,
		estimativa      TEXT NOT NULL,
		estado          TEXT NOT NULL,
		pedido_em       TEXT NOT NULL,
		inicio          TEXT,
		prazo           TEXT,
		fim             TEXT,
		fim_motivo      TEXT NOT NULL DEFAULT '',
		sozinha         INTEGER NOT NULL DEFAULT 0,
		pico            TEXT,
		prazo_avisado   INTEGER NOT NULL DEFAULT 0,
		abaixo_desde    TEXT,
		sem_uso_avisado INTEGER NOT NULL DEFAULT 0,
		chave           TEXT,
		UNIQUE (vaga, chave)
	);
	CREATE INDEX concessoes_recurso ON concessoes (recurso, estado);
	CREATE INDEX concessoes_vaga ON concessoes (vaga, estado);
	-- Picos das concessões que ficaram sozinhas: a base da estimativa por tipo.
	CREATE TABLE historico_recursos (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		recurso TEXT NOT NULL,
		tipo    TEXT NOT NULL,
		pico    TEXT NOT NULL,
		fim     TEXT NOT NULL
	);
	CREATE INDEX historico_recursos_tipo ON historico_recursos (recurso, tipo, id);
	-- Leituras dos medidores (valores é JSON por dimensão), guardadas por 7 dias.
	CREATE TABLE amostras (
		recurso TEXT NOT NULL,
		quando  TEXT NOT NULL,
		valores TEXT NOT NULL
	);
	CREATE INDEX amostras_recurso ON amostras (recurso, quando);
	-- Execuções longas (central rodar): fim nulo enquanto rodam; o batimento recente evita o sumido da vaga.
	CREATE TABLE execucoes (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		vaga      TEXT NOT NULL,
		rotulo    TEXT NOT NULL,
		comando   TEXT NOT NULL,
		inicio    TEXT NOT NULL,
		batimento TEXT NOT NULL,
		fim       TEXT,
		codigo    INTEGER,
		final_log TEXT,
		concessao INTEGER
	);
	CREATE INDEX execucoes_vaga ON execucoes (vaga, fim);
	-- Fatos do projeto: cada gravação é uma linha; o último por chave vence e o resto é histórico.
	CREATE TABLE fatos (
		id     INTEGER PRIMARY KEY AUTOINCREMENT,
		chave  TEXT NOT NULL,
		valor  TEXT NOT NULL,
		vaga   TEXT NOT NULL,
		motivo TEXT NOT NULL,
		quando TEXT NOT NULL
	);
	CREATE INDEX fatos_chave ON fatos (chave, id);
	-- Contadores de ids por prefixo (T-001...).
	CREATE TABLE contadores (
		prefixo TEXT PRIMARY KEY,
		ultimo  INTEGER NOT NULL
	);`,
	// 4: M1b. Processo: área da vaga, itens de trabalho e suas notas, sprints e cerimônias.
	`ALTER TABLE agentes ADD area TEXT NOT NULL DEFAULT '';
	CREATE TABLE itens (
		id             TEXT PRIMARY KEY,
		tipo           TEXT NOT NULL,
		titulo         TEXT NOT NULL DEFAULT '',
		estado         TEXT NOT NULL,
		dono           TEXT NOT NULL DEFAULT '',
		prioridade     TEXT NOT NULL DEFAULT '',
		area           TEXT NOT NULL DEFAULT '',
		categoria      TEXT NOT NULL DEFAULT '',
		sprint         TEXT NOT NULL DEFAULT '',
		arquivos       TEXT NOT NULL DEFAULT '[]',
		recurso        TEXT NOT NULL DEFAULT '',
		folga          INTEGER NOT NULL DEFAULT 0,
		bloqueante     INTEGER NOT NULL DEFAULT 0,
		visivel        INTEGER NOT NULL DEFAULT 0,
		bloqueios      INTEGER NOT NULL DEFAULT 0,
		ponta          TEXT NOT NULL DEFAULT '',
		ponta_revisada TEXT NOT NULL DEFAULT '',
		commit_        TEXT NOT NULL DEFAULT '',
		marco          INTEGER NOT NULL DEFAULT 0,
		campos         TEXT NOT NULL DEFAULT '{}',
		fonte          TEXT NOT NULL DEFAULT '',
		criado_em      TEXT NOT NULL,
		atualizado_em  TEXT NOT NULL,
		estado_desde   TEXT NOT NULL
	);
	CREATE INDEX itens_estado ON itens (estado);
	CREATE TABLE itens_notas (
		id     INTEGER PRIMARY KEY AUTOINCREMENT,
		item   TEXT NOT NULL,
		tipo   TEXT NOT NULL,
		texto  TEXT NOT NULL,
		vaga   TEXT NOT NULL,
		quando TEXT NOT NULL
	);
	CREATE INDEX itens_notas_item ON itens_notas (item, id);
	CREATE TABLE sprints (
		id                TEXT PRIMARY KEY,
		abertura          TEXT NOT NULL,
		checkin           TEXT NOT NULL,
		escopo            TEXT NOT NULL,
		congelamento      TEXT NOT NULL,
		corte             TEXT NOT NULL,
		retro             TEXT NOT NULL,
		fim               TEXT NOT NULL,
		encerrada_em      TEXT,
		antecipada_em     TEXT,
		original          TEXT NOT NULL DEFAULT '{}',
		antecipar_desde   TEXT,
		antecipar_avisado INTEGER NOT NULL DEFAULT 0,
		criada_em         TEXT NOT NULL
	);
	CREATE TABLE cerimonias (
		sprint       TEXT NOT NULL,
		nome         TEXT NOT NULL,
		quando       TEXT NOT NULL,
		disparada_em TEXT NOT NULL,
		PRIMARY KEY (sprint, nome)
	);`,
	// 5: M2. Regras (lições do time) que entram no brief do papel.
	`CREATE TABLE regras (
		id            TEXT PRIMARY KEY,
		origem        TEXT NOT NULL DEFAULT '',
		texto         TEXT NOT NULL DEFAULT '',
		pratica       TEXT NOT NULL,
		aplica        TEXT NOT NULL,
		estado        TEXT NOT NULL,
		comando       TEXT NOT NULL DEFAULT '',
		criada_em     TEXT NOT NULL,
		atualizada_em TEXT NOT NULL
	);`,
	// 6: executor de vagas. A Central abre as vagas como sessões do Claude Code e
	// conduz os turnos delas; despertares são turnos pedidos com hora; o estado do
	// executor (a pausa do plano) é uma linha só.
	`CREATE TABLE vagas_exec (
		vaga           TEXT PRIMARY KEY,
		papel          TEXT NOT NULL,
		area           TEXT NOT NULL DEFAULT '',
		nota           TEXT NOT NULL DEFAULT '',
		sessao         TEXT NOT NULL,
		pasta          TEXT NOT NULL DEFAULT '',
		modelo         TEXT NOT NULL,
		esforco        TEXT NOT NULL,
		estado         TEXT NOT NULL,
		sprint         TEXT NOT NULL DEFAULT '',
		falhas         INTEGER NOT NULL DEFAULT 0,
		falha_anterior TEXT NOT NULL DEFAULT '',
		n_turnos       INTEGER NOT NULL DEFAULT 0,
		proximo_em     TEXT,
		pid            INTEGER NOT NULL DEFAULT 0,
		turno_desde    TEXT,
		turno_motivo   TEXT NOT NULL DEFAULT '',
		ultimo_turno   TEXT,
		ultima_negacao TEXT NOT NULL DEFAULT '',
		custo_dia      REAL NOT NULL DEFAULT 0,
		custo_dia_em   TEXT NOT NULL DEFAULT '',
		aberta_em      TEXT NOT NULL,
		fechada_em     TEXT
	);
	CREATE TABLE despertares (
		id     INTEGER PRIMARY KEY AUTOINCREMENT,
		vaga   TEXT NOT NULL,
		quando TEXT NOT NULL,
		motivo TEXT NOT NULL,
		feito  INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX despertares_pendentes ON despertares (feito, quando);
	CREATE TABLE executor_estado (
		id                INTEGER PRIMARY KEY CHECK (id = 1),
		plano_pausado_ate TEXT,
		motivo            TEXT NOT NULL DEFAULT ''
	);
	INSERT INTO executor_estado (id) VALUES (1);`,
}
