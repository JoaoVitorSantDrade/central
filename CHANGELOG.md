# Mudanças

## 1.1.0 (2026-10-21)

Instalação de um comando e repositório público, com licença MIT.

- **Instaladores de uma linha:** `install.ps1` (Windows) e `install.sh` (Linux e macOS), publicados como assets da release. Eles baixam o binário, conferem o SHA256 e rodam `central instalar`.
- **`central instalar`:** copia o binário para uma pasta do usuário e põe essa pasta no PATH do usuário, sem administrador. Se o `claude` estiver no PATH, também instala o plugin do Claude Code.
- **`central desinstalar`:** desfaz o `instalar`, mantendo os dados.
- **`central atualizar`:** baixa a release, confere o SHA256, confere a versão do binário novo e se troca.
- **`central parar`** e a rota `POST /v1/daemon/encerrar`.
- **Troca automática do daemon:** uma CLI mais nova substitui um daemon de versão mais antiga, e uma CLI antiga nunca derruba um daemon mais novo. Um daemon 1.0.0, que não tem a rota de encerrar, só é encerrado pelo pid quando o `daemon.json` da pasta de dados confirma esse pid.

## 1.0.0 (2026-10-21)

Primeira versão estável. Os contratos `/v1` (goldens em `internal/api/testdata/`), `ext/v1` (`docs/ext-v1.md` e `internal/ext/testdata/`) e o formato do `central.toml` estão congelados. Quebrar qualquer um deles exige uma versão maior (spec §10).

**Núcleo (M0, M1a)**
- Daemon local que sobe sozinho, só em loopback, com um banco SQLite (WAL) por projeto e o registro da máquina.
- Mensagens para vaga, papel ou `TODOS`, e o `esperar` que acorda na hora (mediana de 2,5 s no piloto, sem polling). Respostas com prazo, mensagens da própria Central e idempotência por `--chave`.
- Travas, recursos com dimensões (admissão pelo medido e pelo declarado, medidor, prazo, sem uso, aprendizado do pico) e `rodar`.
- Fatos com histórico, ids por prefixo, backup de hora em hora e antes de migrar, e `doctor`.

**Processo (M1b)**
- Papéis renomeáveis e desligáveis e o ciclo de vida das tarefas e missões. O `proxima` explica a escolha.
- Decisões de duas chaves com veto do humano, e a sprint com cerimônias e encerramento antecipado.
- Adaptador do backlog em arquivos (git), saúde, órfãs, métricas, e `importar-locks` e `exportar-locks` (o formato antigo do LyricCut).

**Contexto (M2)**
- `brief` por papel em 8 blocos, com teto de tokens: quem você é, agora, pendências, papel, núcleo, regras, fatos e mapa de leitura.
- Regras do time a partir das lições, servidor MCP e plugin do Claude Code (skill, MCP e hook pós-resumo).

**Painel (M3)**
- `central painel`: a máquina e, por projeto, as abas Agora, Quadro, Conversa, Linha do tempo, Sprint e Decisões, ao vivo por SSE.
- O humano manda mensagens como `HUMANO`, veta e responde decisões. O painel recusa Host de fora (DNS rebinding) e escrita de outra origem (CSRF).

**Extensões (M4)**
- `ext/v1`: programas declarados no `central.toml`, com subcomandos de CLI, ferramenta MCP, aba no painel (`tabela`, `galeria`, `serie`, `formulario`) e pasta de entrada.
- O formulário de uma extensão fica pendente até o humano responder.
- Eventos e fatos voltam pela resposta, e `[acordar]` diz quem cada evento acorda.
- No LyricCut, as extensões `exemplos` (vídeos-meta e conjuntos de base) e `e2e` (registros de e2e como fatos).
