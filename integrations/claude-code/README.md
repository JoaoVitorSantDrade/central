# Plugin da Central para o Claude Code

Instalar o plugin já traz a Central. Você não precisa instalar o binário antes.
```
/plugin marketplace add JoaoVitorSantDrade/central
/plugin install central@central
```

O que ele traz:
- **A própria Central:**
  - **Shim no PATH:** o `bin/central` entra no PATH do Bash dos agentes enquanto o plugin está ligado.
  - **Instalação na primeira vez:** o shim baixa o binário da última release, confere o SHA256 e roda `central instalar --sem-plugin sim`. O binário vai para `%LOCALAPPDATA%\Programs\central` (ou `~/.local/bin`) e essa pasta entra no PATH do usuário. Depois disso, todas as chamadas vão direto para o binário instalado.
  - **Atualização no início da sessão:** um hook `SessionStart` (`startup`) faz a mesma garantia logo que a sessão abre. Se o binário for mais antigo que a versão do plugin, ele roda `central atualizar`. O hook não escreve nada no contexto da sessão.
- **Skill `central`:** ensina o ciclo do agente (entrar, brief, trabalho pelo papel, `esperar` em segundo plano).
- **Servidor MCP `central`** (`.mcp.json`): as operações de agente como ferramentas (`brief`, `caixa`, `enviar`, `proxima`, `concluir`, `recurso_pedir`, `extensao`…), rodando na pasta do projeto da sessão. Ele chama o `central` pelo PATH. Por isso, **só na primeiríssima instalação** as ferramentas MCP aparecem a partir da próxima vez que o app abrir; a CLI pelo Bash já funciona na mesma sessão.
- **Hook `SessionStart`** (`compact|resume`, depois de um resumo automático ou ao retomar): injeta `central brief --curto --md`. Fora de um projeto com `central.toml`, ou sem a vaga ter entrado, ele não imprime nada.

**Requisitos:** `curl` e `sh`. No Windows, eles vêm com o Git for Windows, que o Claude Code já usa.

**Sem internet, ou com a Central instalada de outro jeito:** o shim usa o binário que já estiver na pasta de instalação. Para testar uma cópia local do plugin:
```
/plugin marketplace add C:\caminho\para\central
/plugin install central@central
```
