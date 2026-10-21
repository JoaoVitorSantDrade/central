# Piloto M0: acordar na hora

Data: 2026-10-21 · Binário `0.1.0` (`m0-piloto`) · Windows 11, app Claude (desktop)

## Pergunta
Uma sessão do app Claude em `/loop` acorda em menos de 10 s quando recebe uma mensagem, **sem polling**, se terminar cada ciclo ocioso com `central esperar` em segundo plano (spec §5.1)?

## Montagem
- Projeto `piloto` com duas pastas (`central-piloto\a` e `central-piloto\b`) que têm o mesmo `central.toml`.
- **Ouvinte** (vaga `B1`): sessão nova do app, Sonnet 5.5 com esforço `low`, em `/loop`. Instrução de fim de ciclo: `central esperar --ate 30m` pelo Bash com `run_in_background: true` e despertar de reserva com ScheduleWakeup de 1 800 s.
- **Emissor** (vaga `A1`): script em segundo plano noutra sessão, com 5 pings a cada 180 s e uma mensagem `fim`.
- **Medida:** `criada_em` do ping → evento `caixa.lida` da vaga `B1` no banco do projeto (gravado pelo daemon, não pelo agente).

## Resultado

| Mensagem | Latência até a `caixa` |
|---|---|
| ping 1 | 2,64 s |
| ping 2 | 2,38 s |
| ping 3 | 2,29 s |
| ping 4 | 3,00 s |
| ping 5 | 2,51 s |
| fim | 2,09 s |

- **Mediana 2,5 s, máximo 3,0 s** (meta: < 10 s). Os ~2 s são o app re-invocando a sessão e o modelo rodando `caixa`; o `esperar` em si volta em milissegundos (teste `TestEsperarAcordaEmMenosDeUmSegundo`).
- **Nenhum despertar sem mensagem:** a única `caixa` vazia foi a da entrada. Nenhum despertar de reserva disparou.
- Todos os 5 ecos chegaram ao emissor.

## Decisão
O mecanismo principal do §5.1 funciona; o plano B (`esperar` curto em primeiro plano) não é necessário.

**Instrução de fim de ciclo para os cartões de papel:**
> Sem trabalho: rode `central esperar --ate 30m` pelo Bash em segundo plano (`run_in_background: true`), agende um despertar de reserva de 30 min (ScheduleWakeup 1800) e termine o turno. Quando o comando terminar, você é re-invocado: comece o ciclo por `central caixa`.

## Não verificado
- Outros agentes além do Claude Code (o `esperar` é um comando que bloqueia, então funciona em qualquer laço, mas não foi testado).
- Muitas sessões esperando ao mesmo tempo (o M1 cobre isso com os testes de concorrência e uma sprint real).
- Comportamento depois de um resumo automático da conversa no meio do loop.
