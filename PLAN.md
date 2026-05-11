# PLAN.md — Garimpo
# Checklist de Implementação

Siga as fases em ordem. Não avance para a próxima fase sem concluir a atual.
Cada item marcado com [ ] é uma tarefa concreta.

---

## FASE 1 — Setup do projeto

- [ ] Criar pasta `garimpo/` e inicializar o módulo Go
  ```bash
  mkdir garimpo && cd garimpo
  go mod init github.com/Gustavo-Resende/garimpo
  ```
- [ ] Criar a estrutura de pastas completa
  ```bash
  mkdir -p cmd/garimpo
  mkdir -p internal/shopee
  mkdir -p internal/gemini
  mkdir -p internal/evolution
  mkdir -p internal/queue
  mkdir -p internal/scheduler
  mkdir -p internal/worker
  ```
- [ ] Criar o arquivo `.env.example` com todas as variáveis (copiar do CLAUDE.md)
- [ ] Criar o arquivo `.env` com as credenciais reais (nunca commitar)
- [ ] Criar o `.gitignore` incluindo `.env` e `garimpo.db`
- [ ] Instalar a única dependência externa necessária:
  ```bash
  go get github.com/mattn/go-sqlite3
  ```
- [ ] Criar o `cmd/garimpo/main.go` vazio com `package main` e `func main() {}`
- [ ] Rodar `go build ./...` e confirmar que compila sem erros

---

## FASE 2 — Banco de dados e fila (queue)

> Prioridade: a fila é o coração do sistema. Implementar e testar antes de qualquer integração externa.

- [ ] Criar `internal/queue/queue.go`
- [ ] Implementar a struct `Product` com todos os campos do schema
- [ ] Implementar a struct `Queue` que recebe `*sql.DB`
- [ ] Implementar `NewQueue(db *sql.DB) *Queue`
- [ ] Implementar `Migrate(db *sql.DB) error` que roda o `CREATE TABLE IF NOT EXISTS`
- [ ] Implementar `Enqueue(p Product) error`
  - Usar `INSERT OR IGNORE` para respeitar o UNIQUE em `offer_link`
- [ ] Implementar `Dequeue() (*Product, error)`
  - Busca o produto mais antigo com `status = 'pending'`
  - Retorna `nil, nil` se a fila estiver vazia
- [ ] Implementar `MarkSent(id int) error`
- [ ] Implementar `MarkFailed(id int) error`
- [ ] Implementar `CountPending() (int, error)` (útil para logs)
- [ ] Testar manualmente: criar um `main.go` temporário que enfileira e desenfileira produtos, confirmar que funciona

---

## FASE 3 — Cliente Shopee

> Implementar a autenticação SHA256 e a query de produtos.

- [ ] Criar `internal/shopee/client.go`
- [ ] Implementar a struct `Client` com `appID`, `secret` e `httpClient`
- [ ] Implementar `NewClient(appID, secret string) *Client`
- [ ] Implementar a função de assinatura `sign(payload string) (timestamp, signature string)`
  - Concatenar `appID + timestamp + payload + secret`
  - Gerar SHA256 em hex
- [ ] Implementar `buildAuthHeader(payload string) string`
  - Retorna o header no formato: `SHA256 Credential={appID}, Timestamp={ts}, Signature={sig}`
- [ ] Definir as structs de resposta GraphQL:
  - `ProductNode` com todos os campos retornados
  - `ProductOfferResponse` mapeando a estrutura completa da resposta
- [ ] Implementar `FetchProducts(minCommission float64, limit int) ([]ProductNode, error)`
  - Monta a query GraphQL com `sortType: 2` e `limit` configurável
  - Faz o POST com o header de autenticação
  - Filtra produtos com `commissionRate >= minCommission`
  - Retorna erro descritivo se a resposta contiver `errors`
- [ ] Testar: rodar uma chamada real e logar os produtos retornados
- [ ] Confirmar que a filtragem por comissão mínima funciona

---

## FASE 4 — Cliente Gemini

> Gerar a mensagem de divulgação a partir dos dados do produto.

- [ ] Criar `internal/gemini/client.go`
- [ ] Implementar a struct `Client` com `apiKey` e `httpClient`
- [ ] Implementar `NewClient(apiKey string) *Client`
- [ ] Definir a struct do body da requisição Gemini e da resposta
- [ ] Escrever o prompt base como constante no pacote:
  - Instrução de tom (informal, animado, português brasileiro)
  - Instrução de formato (máx 5 linhas, emojis, link no final)
  - Instrução de não inventar informações
- [ ] Implementar `GenerateMessage(p queue.Product) (string, error)`
  - Monta o prompt com os dados reais do produto
  - Chama a API Gemini 2.5 Flash
  - Extrai o texto da resposta
  - Retorna erro descritivo em caso de falha
- [ ] Testar: passar um produto fictício e confirmar que a mensagem gerada está no formato esperado
- [ ] Ajustar o prompt se necessário até o resultado ficar bom

---

## FASE 5 — Cliente Evolution API

> Enviar a mensagem gerada para o grupo de WhatsApp.

- [ ] Criar `internal/evolution/client.go`
- [ ] Implementar a struct `Client` com `baseURL`, `apiKey`, `instanceName`, `groupJID` e `httpClient`
- [ ] Implementar `NewClient(baseURL, apiKey, instanceName, groupJID string) *Client`
- [ ] Definir a struct do body de envio
- [ ] Implementar `SendMessage(text string) error`
  - POST para `/message/sendText/{instanceName}`
  - Header `apikey: {apiKey}`
  - Body com `number: groupJID` e `text: mensagem`
  - Retorna erro descritivo se status != 2xx
- [ ] Testar: enviar uma mensagem de teste para o grupo e confirmar o recebimento no WhatsApp

---

## FASE 6 — Scheduler (janela horária)

- [ ] Criar `internal/scheduler/scheduler.go`
- [ ] Implementar `IsWithinWindow(startHour, endHour int) bool`
  - Verifica se a hora atual (horário local da VPS) está entre `startHour` e `endHour`
  - Ex: `IsWithinWindow(7, 23)` retorna `true` entre 07:00 e 22:59
- [ ] Testar com horas mockadas para cobrir os casos limite (06:59, 07:00, 22:59, 23:00)

---

## FASE 7 — Workers

> Orquestrar tudo. Aqui os pacotes anteriores se conectam.

### Extractor
- [ ] Criar `internal/worker/extractor.go`
- [ ] Implementar `RunExtractor(shopeeClient, queue, cfg, logger)`
  - Loop com `time.Ticker` de 4h
  - Chama `shopeeClient.FetchProducts()`
  - Para cada produto retornado, chama `queue.Enqueue()`
  - Loga quantos produtos foram adicionados e quantos já existiam (ignorados)
  - Captura erros e loga sem derrubar o processo

### Poster
- [ ] Criar `internal/worker/poster.go`
- [ ] Implementar `RunPoster(queue, geminiClient, evolutionClient, scheduler, cfg, logger)`
  - Loop com `time.Ticker` de 12min
  - Verifica `scheduler.IsWithinWindow()` — se fora da janela, loga e pula
  - Chama `queue.Dequeue()` — se fila vazia, loga e pula
  - Chama `geminiClient.GenerateMessage(product)`
  - Chama `evolutionClient.SendMessage(message)`
  - Em caso de sucesso: `queue.MarkSent(id)`
  - Em caso de erro em qualquer etapa: `queue.MarkFailed(id)`, loga o erro

---

## FASE 8 — Main e injeção de dependências

- [ ] Implementar o `cmd/garimpo/main.go` completo:
  - [ ] Carregar todas as variáveis de ambiente (com validação: se faltar alguma obrigatória, logar e sair)
  - [ ] Abrir conexão SQLite com `sql.Open`
  - [ ] Rodar `queue.Migrate(db)`
  - [ ] Instanciar todos os clientes
  - [ ] Instanciar o scheduler
  - [ ] Disparar `go worker.RunExtractor(...)` em goroutine
  - [ ] Disparar `go worker.RunPoster(...)` em goroutine
  - [ ] Bloquear o processo com `select {}`
- [ ] Rodar `go build ./...` e confirmar que compila sem erros
- [ ] Rodar o binário localmente e confirmar nos logs que os dois workers iniciaram

---

## FASE 9 — Teste end-to-end

> Testar o fluxo completo antes de subir para produção.

- [ ] Com o binário rodando, aguardar o extractor executar e confirmar produtos na fila
  - Verificar no SQLite: `SELECT * FROM queue WHERE status = 'pending';`
- [ ] Aguardar o poster executar (ou reduzir o ticker temporariamente para 1min)
- [ ] Confirmar que a mensagem chegou no grupo do WhatsApp
- [ ] Confirmar que o produto foi marcado como `sent` no banco
- [ ] Testar o caso de fila vazia: confirmar que o poster loga e pula sem erros
- [ ] Testar a janela horária: rodar fora do horário permitido e confirmar que não posta
- [ ] Testar deduplicação: rodar o extractor duas vezes e confirmar que não duplica produtos

---

## FASE 10 — Deploy na VPS Hostinger

- [ ] Acessar a VPS via SSH
- [ ] Instalar Go na VPS (se necessário)
- [ ] Clonar o repositório
- [ ] Criar o `.env` de produção na VPS
- [ ] Compilar o binário na VPS:
  ```bash
  go build -o garimpo ./cmd/garimpo
  ```
- [ ] Criar o service file do systemd em `/etc/systemd/system/garimpo.service`:
  ```ini
  [Unit]
  Description=Garimpo - Bot de Ofertas WPP
  After=network.target

  [Service]
  Type=simple
  WorkingDirectory=/home/seu-usuario/garimpo
  ExecStart=/home/seu-usuario/garimpo/garimpo
  Restart=on-failure
  RestartSec=10

  [Install]
  WantedBy=multi-user.target
  ```
- [ ] Habilitar e iniciar o serviço:
  ```bash
  sudo systemctl enable garimpo
  sudo systemctl start garimpo
  sudo systemctl status garimpo
  ```
- [ ] Confirmar nos logs que os workers estão rodando:
  ```bash
  journalctl -u garimpo -f
  ```
- [ ] Aguardar o primeiro ciclo completo em produção e confirmar mensagem no grupo

---

## Checklist de saúde pós-deploy

- [ ] Produto postado no grupo com mensagem formatada corretamente
- [ ] Nenhum post fora da janela 07:00-23:00
- [ ] Nenhum produto duplicado na fila
- [ ] Processo sobrevive a restart (systemd reinicia automaticamente)
- [ ] Logs legíveis e sem erros recorrentes
