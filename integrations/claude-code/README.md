# Plugin da Central para o Claude Code

O que ele traz:
- **Skill `central`**: ensina o ciclo do agente (entrar, brief, trabalho pelo papel, `esperar` em segundo plano).
- **Servidor MCP `central`** (`.mcp.json`): as operações de agente como ferramentas (`brief`, `caixa`, `enviar`, `proxima`, `concluir`, `recurso_pedir`…). O servidor roda na pasta do projeto da sessão.
- **Hook `SessionStart`** (depois de um resumo automático ou ao retomar): injeta `central brief --curto --md`. Fora de um projeto com `central.toml`, ou sem a vaga ter entrado, ele não imprime nada.

## Instalar
O binário `central` precisa estar no `PATH` (`go build -o central.exe ./cmd/central` na raiz do repositório).

Pelo marketplace deste repositório:
```
/plugin marketplace add JoaoVitorSantDrade/central
/plugin install central@central
```
Ou, local, apontando para esta pasta:
```
/plugin marketplace add C:\caminho\para\central
/plugin install central@central
```
