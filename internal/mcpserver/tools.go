package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donwellus/aiw/internal/clean"
	"github.com/donwellus/aiw/internal/control"
	"github.com/donwellus/aiw/internal/pause"
	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/report"
	"github.com/donwellus/aiw/internal/runview"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/sources"
	"github.com/donwellus/aiw/internal/taskcreate"
)

func toolDefinitions(allowWrite bool) []map[string]any {
	readOnly := map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
	tools := []map[string]any{
		{
			"name":        "situation",
			"title":       "Situação completa",
			"description": "Snapshot determinístico de daemon, capacidade, fila, runs ativas/pausadas, operações, claims e alertas.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": readOnly,
		},
		{
			"name":        "status",
			"title":       "Status do aiw",
			"description": "Resumo do estado atual: pausa global, contagem por status, tarefas ativas, claims e alertas.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": readOnly,
		},
		{
			"name":        "report",
			"title":       "Relatório do aiw",
			"description": "Relatório completo read-only de tarefas, runs, custos, claims e alertas. Use quando o humano pedir report, relatório, panorama ou resumo geral; summary_only retorna a visão rápida.",
			"inputSchema": objectSchema(map[string]any{
				"summary_only": map[string]any{"type": "boolean", "description": "Retorna apenas resumo e alertas"},
			}),
			"annotations": readOnly,
		},
		map[string]any{
			"name":        "ci_status",
			"title":       "Status da correção de CI",
			"description": "Mostra o estado do loop de correção de CI de uma run, incluindo blockers e feedback humano.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string", "description": "ID da run"},
			}, "run_id"),
			"annotations": readOnly,
		},
		{
			"name":        "runs",
			"title":       "Listar runs",
			"description": "Lista runs registradas, mais recentes primeiro.",
			"inputSchema": objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
				"status": map[string]any{"type": "string", "description": "Filtra por status exato"},
				"all":    map[string]any{"type": "boolean", "description": "Inclui tentativas antigas/superadas"},
			}),
			"annotations": readOnly,
		},
		{
			"name":        "run_show",
			"title":       "Detalhar run",
			"description": "Mostra run.json e os últimos eventos de uma execução.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":       map[string]any{"type": "string", "description": "ID da run"},
				"events_limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			}, "run_id"),
			"annotations": readOnly,
		},
		{
			"name":        "events",
			"title":       "Eventos recentes",
			"description": "Lista eventos recentes de uma run ou de todas as runs.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Opcional: limita a uma run"},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			}),
			"annotations": readOnly,
		},
		{
			"name":        "daemon_status",
			"title":       "Status do daemon",
			"description": "Mostra se o daemon está ativo e seu heartbeat.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": readOnly,
		},
		{
			"name":        "operations",
			"title":       "Listar operações",
			"description": "Lista operações assíncronas de controle, mais recentes primeiro.",
			"inputSchema": objectSchema(map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 20},
			}),
			"annotations": readOnly,
		},
		{
			"name":        "operation_show",
			"title":       "Detalhar operação",
			"description": "Mostra o estado e o resultado de uma operação assíncrona.",
			"inputSchema": objectSchema(map[string]any{
				"operation_id": map[string]any{"type": "string", "description": "ID retornado por resume"},
			}, "operation_id"),
			"annotations": readOnly,
		},
		{
			"name":        "questions",
			"title":       "Perguntas da run",
			"description": "Lê questions.md e intake.md de uma run que precisa de input.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string"},
			}, "run_id"),
			"annotations": readOnly,
		},
		{
			"name":        "clean_preview",
			"title":       "Prévia de limpeza",
			"description": "Mostra o que o aiw clean removeria, sem alterar nada.",
			"inputSchema": cleanSchema(false),
			"annotations": readOnly,
		},
		{
			"name":        "refine_preview",
			"title":       "Prévia de refine",
			"description": "Mostra o feedback, o alvo e o impacto do refine sem enfileirar nem alterar a run.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":   map[string]any{"type": "string"},
				"target":   map[string]any{"type": "string", "enum": []string{"intake", "spec", "plan"}, "description": "Artefato mais antigo invalidado"},
				"feedback": map[string]any{"type": "string", "description": "Texto proposto para o refine"},
			}, "run_id", "target", "feedback"),
			"annotations": readOnly,
		},
		{
			"name":        "sources",
			"title":       "Fontes configuradas",
			"description": "Lista fontes configuradas, a fonte ativa e quais suportam criação de tarefas.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": readOnly,
		},
		{
			"name":        "prepare_issue",
			"title":       "Preparar issue",
			"description": "Valida e formata um draft de issue sem criar nada; retorna campos faltantes e preview_id.",
			"inputSchema": issueDraftSchema(false),
			"annotations": readOnly,
		},
	}
	if !allowWrite {
		return tools
	}
	return append(tools,
		map[string]any{
			"name":        "pause",
			"title":       "Pausar",
			"description": "Pausa uma run ou todo o projeto (sem run_id).",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Opcional: pausa só esta run"},
				"reason": map[string]any{"type": "string", "description": "Motivo da pausa"},
			}),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
		},
		map[string]any{
			"name":        "unpause",
			"title":       "Remover pausa global",
			"description": "Remove somente a pausa global; não retoma nenhuma run pausada.",
			"inputSchema": objectSchema(map[string]any{}),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
		},
		map[string]any{
			"name":        "resume",
			"title":       "Retomar",
			"description": "Enfileira resume de uma run ou de todas as paused/canceled. Requer daemon ativo.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":     map[string]any{"type": "string", "description": "Run específica"},
				"all":        map[string]any{"type": "boolean", "description": "Retoma todas as paused/canceled"},
				"auto":       map[string]any{"type": "boolean", "description": "Aprova gates automaticamente"},
				"budget_usd": map[string]any{"type": "number", "minimum": 0, "description": "Orçamento da execução"},
			}),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		map[string]any{
			"name":        "approve",
			"title":       "Aprovar gate",
			"description": "Aprova um gate intake, spec ou plan.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string"},
				"gate":   map[string]any{"type": "string", "enum": []string{"intake", "spec", "plan"}},
			}, "run_id", "gate"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
		},
		map[string]any{
			"name":        "release",
			"title":       "Liberar run",
			"description": "Libera claim e estado de uma run morta. Pede confirmação no OpenCode.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string"},
			}, "run_id"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false},
		},
		map[string]any{
			"name":        "clean_apply",
			"title":       "Aplicar limpeza",
			"description": "Remove worktrees/branches elegíveis. Exige confirm=true e confirmação no OpenCode.",
			"inputSchema": cleanSchema(true),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false},
		},
		map[string]any{
			"name":        "cancel",
			"title":       "Cancelar run",
			"description": "Cancela uma run em andamento ou uma run pausada/needs_input.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string"},
			}, "run_id"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false},
		},
		map[string]any{
			"name":        "refine",
			"title":       "Refinar gate",
			"description": "Refina spec ou plan de uma run pausada, com target explícito. Requer daemon ativo.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":     map[string]any{"type": "string"},
				"target":     map[string]any{"type": "string", "enum": []string{"intake", "spec", "plan"}, "description": "Artefato mais antigo invalidado"},
				"feedback":   map[string]any{"type": "string", "description": "Refinamento humano"},
				"auto":       map[string]any{"type": "boolean", "description": "Aprova gates automáticos após regenerar"},
				"budget_usd": map[string]any{"type": "number", "minimum": 0},
			}, "run_id", "target", "feedback"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		map[string]any{
			"name":        "answer",
			"title":       "Responder needs_input",
			"description": "Publica a resposta na fonte e enfileira um rerun no daemon.",
			"inputSchema": objectSchema(map[string]any{
				"run_id": map[string]any{"type": "string"},
				"text":   map[string]any{"type": "string", "description": "Resposta fornecida pelo humano"},
			}, "run_id", "text"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		map[string]any{
			"name":        "create_issue",
			"title":       "Criar issue",
			"description": "Cria uma issue/tarefa na fonte ativa após validar o draft. Campos incompletos exigem allow_incomplete e acknowledged_missing.",
			"inputSchema": issueDraftSchema(true),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		map[string]any{
			"name":        "close_issue",
			"title":       "Fechar issue",
			"description": "Fecha uma issue/tarefa na fonte ativa. Se houver run ativa, exige cancel_run=true e só fecha depois do cancelamento confirmado.",
			"inputSchema": objectSchema(map[string]any{
				"issue_id":   map[string]any{"type": "string", "description": "ID da issue/tarefa"},
				"reason":     map[string]any{"type": "string", "description": "Motivo publicado no fechamento"},
				"cancel_run": map[string]any{"type": "boolean", "description": "Cancela a run ativa antes; o fechamento só ocorre após confirmação"},
			}, "issue_id", "reason"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false},
		},
		map[string]any{
			"name":        "ci_decision",
			"title":       "Decidir correção de CI",
			"description": "Registra a decisão humana para uma run pausada em delivery e enfileira o resume. Use FIX_CODE, FIX_TEST ou RETRY_CI.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":   map[string]any{"type": "string", "description": "ID da run pausada"},
				"decision": map[string]any{"type": "string", "enum": []string{"FIX_CODE", "FIX_TEST", "RETRY_CI"}, "description": "Decisão humana"},
				"feedback": map[string]any{"type": "string", "description": "Justificativa e instruções para o agente de qualidade"},
			}, "run_id", "decision"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		map[string]any{
			"name":        "operation_cancel",
			"title":       "Cancelar operação",
			"description": "Cancela uma operação assíncrona pendente ou em execução.",
			"inputSchema": objectSchema(map[string]any{
				"operation_id": map[string]any{"type": "string"},
			}, "operation_id"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
	)
}

func issueDraftSchema(create bool) map[string]any {
	if create {
		return objectSchema(map[string]any{
			"preview_id":           map[string]any{"type": "string", "description": "ID retornado por prepare_issue"},
			"allow_incomplete":     map[string]any{"type": "boolean", "description": "Permite criar com campos faltantes de propósito"},
			"acknowledged_missing": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Lista exata de campos faltantes confirmados"},
		}, "preview_id")
	}
	return objectSchema(map[string]any{
		"title":                  map[string]any{"type": "string", "description": "Título da issue"},
		"description":            map[string]any{"type": "string", "description": "Contexto e objetivo"},
		"acceptance":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Critérios de aceite verificáveis"},
		"labels":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"priority":               map[string]any{"type": "string"},
		"assignee":               map[string]any{"type": "string"},
		"repo":                   map[string]any{"type": "string", "description": "Repositório local/hint; default da source/workspace"},
		"attachments":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Caminhos locais de imagens; exigem autorização explícita"},
		"attachments_authorized": map[string]any{"type": "boolean", "description": "Confirma que o humano autorizou explicitamente os paths"},
	}, "title")
}

func cleanSchema(apply bool) map[string]any {
	properties := map[string]any{
		"force":           map[string]any{"type": "boolean", "description": "Remove worktrees sujos e branches não merged"},
		"keep_branches":   map[string]any{"type": "boolean", "description": "Preserva branches"},
		"skip_superseded": map[string]any{"type": "boolean", "description": "Não limpa runs superadas"},
		"status":          map[string]any{"type": "string", "description": "Status terminais separados por vírgula"},
	}
	required := []string{}
	if apply {
		properties["confirm"] = map[string]any{"type": "boolean", "description": "Confirma a remoção real"}
		required = append(required, "confirm")
	}
	return objectSchema(properties, required...)
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, invalidParams(err)
	}
	if !s.allowWrite && writeTool(params.Name) {
		return nil, &rpcError{Code: -32602, Message: "escrita desabilitada; use aiw mcp --allow-write", Data: params.Name}
	}
	switch params.Name {
	case "situation":
		return s.toolSituation()
	case "status":
		return s.toolStatus(ctx)
	case "report":
		return s.toolReport(ctx, params.Arguments)
	case "ci_status":
		return s.toolCIStatus(params.Arguments)
	case "ci_decision":
		return s.toolCIDecision(params.Arguments)
	case "runs":
		return s.toolRuns(ctx, params.Arguments)
	case "run_show":
		return s.toolRunShow(ctx, params.Arguments)
	case "events":
		return s.toolEvents(ctx, params.Arguments)
	case "daemon_status":
		return s.toolDaemonStatus()
	case "questions":
		return s.toolQuestions(params.Arguments)
	case "operations":
		return s.toolOperations(params.Arguments)
	case "operation_show":
		return s.toolOperationShow(params.Arguments)
	case "clean_preview":
		return s.toolClean(ctx, params.Arguments, false)
	case "sources":
		return s.toolSources()
	case "prepare_issue":
		return s.toolPrepareIssue(ctx, params.Arguments)
	case "pause":
		return s.toolPause(params.Arguments)
	case "unpause":
		return s.toolUnpause()
	case "cancel":
		return s.toolCancel(ctx, params.Arguments)
	case "resume":
		return s.toolResume(params.Arguments)
	case "approve":
		return s.toolApprove(params.Arguments)
	case "answer":
		return s.toolAnswer(ctx, params.Arguments)
	case "refine_preview":
		return s.toolRefinePreview(params.Arguments)
	case "refine":
		return s.toolRefine(params.Arguments)
	case "create_issue":
		return s.toolCreateIssue(ctx, params.Arguments)
	case "close_issue":
		return s.toolCloseIssue(ctx, params.Arguments)
	case "release":
		return s.toolRelease(ctx, params.Arguments)
	case "clean_apply":
		return s.toolClean(ctx, params.Arguments, true)
	case "operation_cancel":
		return s.toolOperationCancel(params.Arguments)
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool", Data: params.Name}
	}
}

func writeTool(name string) bool {
	switch name {
	case "pause", "unpause", "resume", "approve", "release", "clean_apply", "operation_cancel", "cancel", "answer", "refine", "create_issue", "close_issue", "ci_decision":
		return true
	default:
		return false
	}
}

func (s *Server) toolSources() (any, *rpcError) {
	registry := sources.NewRegistry(s.cfg)
	info, active := control.DaemonActive(s.cfg, 15*time.Second)
	activeSource := s.cfg.DefaultSource
	if active && strings.TrimSpace(info.Source) != "" {
		activeSource = info.Source
	}
	names := make([]string, 0, len(s.cfg.Sources))
	for name := range s.cfg.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		sc := s.cfg.Sources[name]
		item := map[string]any{"name": name, "driver": sc.Driver, "default": name == s.cfg.DefaultSource, "active": name == activeSource, "labels": sc.Labels, "repo_path": sc.RepoPath}
		src, err := registry.Open(name)
		if err != nil {
			item["error"] = err.Error()
			items = append(items, item)
			continue
		}
		_, canCreate := src.(source.TaskCreator)
		item["can_create"] = canCreate
		item["caps"] = src.Caps()
		items = append(items, item)
	}
	value, err := toolResult(map[string]any{
		"active_source": activeSource,
		"daemon_active": active,
		"sources":       items,
	})
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) toolPrepareIssue(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var draft source.TaskDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, invalidParams(err)
	}
	preview, err := taskcreate.New(s.cfg).Prepare(ctx, draft)
	if err != nil {
		return toolError(err.Error()), nil
	}
	s.rememberIssuePreview(preview)
	value, err := toolResult(preview)
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) toolCreateIssue(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		PreviewID           string   `json:"preview_id"`
		AllowIncomplete     bool     `json:"allow_incomplete"`
		AcknowledgedMissing []string `json:"acknowledged_missing"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	preview, err := s.loadIssuePreview(args.PreviewID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	result, err := taskcreate.New(s.cfg).Create(ctx, taskcreate.Request{
		Draft:               preview.Draft,
		PreviewID:           preview.PreviewID,
		AllowIncomplete:     args.AllowIncomplete,
		AcknowledgedMissing: args.AcknowledgedMissing,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	s.forgetIssuePreview(args.PreviewID)
	value, err := toolResult(result)
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) rememberIssuePreview(preview *taskcreate.Preview) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.previews == nil {
		s.previews = map[string]cachedIssuePreview{}
	}
	now := time.Now().UTC()
	for id, cached := range s.previews {
		if now.Sub(cached.createdAt) > time.Hour {
			delete(s.previews, id)
		}
	}
	s.previews[preview.PreviewID] = cachedIssuePreview{preview: *preview, createdAt: now}
}

func (s *Server) loadIssuePreview(id string) (*taskcreate.Preview, error) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	id = strings.TrimSpace(id)
	cached, ok := s.previews[id]
	if !ok || id == "" {
		return nil, errors.New("preview expirado ou inválido; gere um novo preview")
	}
	if time.Since(cached.createdAt) > time.Hour {
		delete(s.previews, id)
		return nil, errors.New("preview expirado ou inválido; gere um novo preview")
	}
	preview := cached.preview
	return &preview, nil
}

func (s *Server) forgetIssuePreview(id string) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	delete(s.previews, id)
}

func (s *Server) toolCloseIssue(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		IssueID   string `json:"issue_id"`
		Reason    string `json:"reason"`
		CancelRun bool   `json:"cancel_run"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	result, err := control.CloseIssue(ctx, s.cfg, s.store, args.IssueID, args.Reason, args.CancelRun)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) toolDaemonStatus() (any, *rpcError) {
	info, active := control.DaemonActive(s.cfg, 15*time.Second)
	value := map[string]any{"active": active}
	if info.PID != 0 {
		value["daemon"] = info
	}
	result, err := toolResult(value)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolOperations(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		Limit int `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	all, err := store.List(0)
	if err != nil {
		return toolError(err.Error()), nil
	}
	total := len(all)
	if len(all) > limit {
		all = all[:limit]
	}
	result, err := toolResult(map[string]any{"total": total, "operations": all})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolOperationShow(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	operation, err := store.Get(args.OperationID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	result, err := toolResult(operation)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolQuestions(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if !validRunID(args.RunID) {
		return nil, invalidParams(errors.New("run_id inválido"))
	}
	dir := filepath.Join(s.cfg.RunsDir(), args.RunID)
	value := map[string]any{"run_id": args.RunID}
	found := false
	for _, name := range []string{"questions.md", "assumptions.md", "intake.md", "answer.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return toolError(err.Error()), nil
		}
		value[strings.TrimSuffix(name, ".md")] = string(data)
		found = true
	}
	if !found {
		return toolError("nenhum questions.md/intake.md encontrado para a run"), nil
	}
	result, err := toolResult(value)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolCancel(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	result, err := control.Cancel(ctx, s.cfg, s.store, args.RunID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolAnswer(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	result, err := control.Answer(ctx, s.cfg, args.RunID, args.Text)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolRefinePreview(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID    string `json:"run_id"`
		Target   string `json:"target"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	target := strings.ToLower(strings.TrimSpace(args.Target))
	if target != control.TargetIntake && target != control.TargetSpec && target != control.TargetPlan {
		return nil, invalidParams(errors.New("target inválido (use intake, spec ou plan)"))
	}
	if strings.TrimSpace(args.Feedback) == "" {
		return nil, invalidParams(errors.New("feedback vazio"))
	}
	preview, err := control.PreviewRefine(s.cfg, args.RunID, target, args.Feedback)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(preview)
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) toolRefine(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID     string  `json:"run_id"`
		Target    string  `json:"target"`
		Feedback  string  `json:"feedback"`
		PreviewID string  `json:"preview_id"`
		Auto      bool    `json:"auto"`
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	target := strings.ToLower(strings.TrimSpace(args.Target))
	if target != control.TargetIntake && target != control.TargetSpec && target != control.TargetPlan {
		return nil, invalidParams(errors.New("target inválido (use intake, spec ou plan)"))
	}
	if strings.TrimSpace(args.Feedback) == "" {
		return nil, invalidParams(errors.New("feedback vazio"))
	}
	if _, err := control.ValidateRefinePreview(s.cfg, args.RunID, target, args.Feedback, args.PreviewID); err != nil {
		return toolError(err.Error()), nil
	}
	if _, active := control.DaemonActive(s.cfg, 15*time.Second); !active {
		return toolError("daemon não está ativo; inicie aiw daemon para processar refinamentos"), nil
	}
	ops, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	operation, err := ops.Enqueue(control.ActionRefine, args.RunID, map[string]any{
		"target":     target,
		"feedback":   args.Feedback,
		"preview_id": strings.TrimSpace(args.PreviewID),
		"auto":       args.Auto,
		"budget_usd": args.BudgetUSD,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(map[string]any{"operation": operation})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolPause(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID  string `json:"run_id"`
		Reason string `json:"reason"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	if args.RunID != "" {
		if err := control.ValidateRunID(args.RunID); err != nil {
			return nil, invalidParams(err)
		}
	}
	result, err := control.Pause(s.cfg, args.RunID, args.Reason)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolUnpause() (any, *rpcError) {
	result, err := control.Unpause(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolResume(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID     string  `json:"run_id"`
		All       bool    `json:"all"`
		Auto      bool    `json:"auto"`
		BudgetUSD float64 `json:"budget_usd"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	if args.All == (args.RunID != "") {
		return nil, invalidParams(errors.New("use run_id ou all=true, não ambos"))
	}
	if args.RunID != "" {
		if err := control.ValidateRunID(args.RunID); err != nil {
			return nil, invalidParams(err)
		}
	}
	if !args.All && pause.Active(s.cfg.DataDir()) {
		return toolError("pausa global ativa; use resume com all=true"), nil
	}
	info, active := control.DaemonActive(s.cfg, 15*time.Second)
	if !active {
		return toolError("daemon não está ativo; inicie aiw daemon para processar operações assíncronas"), nil
	}
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	action := control.ActionResume
	if args.All {
		action = control.ActionResumeAll
	}
	operation, err := store.Enqueue(action, args.RunID, map[string]any{
		"auto":       args.Auto,
		"budget_usd": args.BudgetUSD,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(map[string]any{"operation": operation, "daemon": info})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolApprove(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
		Gate  string `json:"gate"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	run, err := control.Approve(s.cfg, args.RunID, args.Gate)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(map[string]any{"run_id": run.ID, "gate": args.Gate, "gates": run.Gates})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolRelease(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	result, err := control.Release(ctx, s.cfg, s.store, args.RunID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolClean(ctx context.Context, raw json.RawMessage, apply bool) (any, *rpcError) {
	var args struct {
		Force          bool   `json:"force"`
		KeepBranches   bool   `json:"keep_branches"`
		SkipSuperseded bool   `json:"skip_superseded"`
		Status         string `json:"status"`
		Confirm        bool   `json:"confirm"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	if apply && !args.Confirm {
		return nil, invalidParams(errors.New("confirm=true obrigatório para clean_apply"))
	}
	statuses, err := clean.ParseStatuses(args.Status)
	if err != nil {
		return nil, invalidParams(err)
	}
	result, err := clean.Run(ctx, s.cfg, s.store, clean.Options{
		Statuses:       statuses,
		DryRun:         !apply,
		Force:          args.Force,
		KeepBranches:   args.KeepBranches,
		SkipSuperseded: args.SkipSuperseded,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(result)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolOperationCancel(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	operation, err := store.Cancel(args.OperationID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(operation)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return value, nil
}

func (s *Server) toolStatus(ctx context.Context) (any, *rpcError) {
	built, err := report.Build(s.cfg, s.store, report.Options{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	active := make([]report.Task, 0)
	for _, task := range built.Tasks {
		switch task.Status {
		case "running", "queued", "pr_open", "paused", "needs_input":
			active = append(active, task)
		}
	}
	result, err := toolResult(map[string]any{
		"generated_at": built.GeneratedAt,
		"root":         built.Root,
		"paused":       built.Paused,
		"summary":      built.Summary,
		"active":       active,
		"claims":       built.Claims,
		"alerts":       built.Alerts,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolReport(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		SummaryOnly bool `json:"summary_only"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	built, err := report.Build(s.cfg, s.store, report.Options{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	var value any = built
	if args.SummaryOnly {
		value = map[string]any{
			"generated_at": built.GeneratedAt,
			"root":         built.Root,
			"paused":       built.Paused,
			"source":       built.Source,
			"summary":      built.Summary,
			"alerts":       built.Alerts,
		}
	}
	result, err := toolResult(value)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

type runSummary struct {
	RunID        string    `json:"run_id"`
	Current      bool      `json:"current"`
	SupersededBy string    `json:"superseded_by,omitempty"`
	TaskID       string    `json:"task_id"`
	Title        string    `json:"title"`
	Source       string    `json:"source,omitempty"`
	Status       string    `json:"status"`
	Stage        string    `json:"stage,omitempty"`
	Repo         string    `json:"repo,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Worktree     string    `json:"worktree,omitempty"`
	CostUSD      float64   `json:"cost_usd"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	PRURL        string    `json:"pr_url,omitempty"`
	Error        string    `json:"error,omitempty"`
}

func (s *Server) toolRuns(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		Limit  int    `json:"limit"`
		Status string `json:"status"`
		All    bool   `json:"all"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	entries, err := runview.Build(s.cfg, s.store)
	if err != nil {
		return toolError(err.Error()), nil
	}
	if !args.All {
		entries = runview.Current(entries)
	}
	filtered := make([]runview.Entry, 0, len(entries))
	for _, entry := range entries {
		if args.Status != "" && entry.Run.Status != args.Status {
			continue
		}
		filtered = append(filtered, entry)
	}
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out := make([]runSummary, 0, len(filtered))
	for _, entry := range filtered {
		run := entry.Run
		out = append(out, runSummary{
			RunID:        run.ID,
			Current:      entry.Current,
			SupersededBy: entry.SupersededBy,
			TaskID:       run.Task.ExternalID,
			Title:        run.Task.Title,
			Source:       run.Source,
			Status:       run.Status,
			Stage:        run.Stage,
			Repo:         run.Repo,
			Branch:       run.Branch,
			Worktree:     run.Worktree,
			CostUSD:      run.Usage.CostUSD,
			InputTokens:  run.Usage.InputTokens,
			OutputTokens: run.Usage.OutputTokens,
			CreatedAt:    run.CreatedAt,
			UpdatedAt:    run.UpdatedAt,
			PRURL:        run.PRURL,
			Error:        run.Error,
		})
	}
	result, err := toolResult(map[string]any{"total": total, "runs": out})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolRunShow(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID       string `json:"run_id"`
		EventsLimit int    `json:"events_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if !validRunID(args.RunID) {
		return nil, invalidParams(errors.New("run_id inválido"))
	}
	run, err := pipeline.LoadRun(s.cfg, args.RunID)
	if err != nil {
		return toolError("run não encontrada: " + err.Error()), nil
	}
	limit := args.EventsLimit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	events := s.readRunEvents(args.RunID)
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	result, err := toolResult(map[string]any{"run": run, "events": events})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) toolEvents(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
		Limit int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, invalidParams(err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var events []map[string]any
	if args.RunID != "" {
		if !validRunID(args.RunID) {
			return nil, invalidParams(errors.New("run_id inválido"))
		}
		events = s.readRunEvents(args.RunID)
	} else {
		entries, err := os.ReadDir(s.cfg.RunsDir())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return toolError(err.Error()), nil
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			events = append(events, s.readRunEvents(entry.Name())...)
		}
	}
	sort.Slice(events, func(i, j int) bool {
		left, _ := events[i]["time"].(string)
		right, _ := events[j]["time"].(string)
		if left == right {
			return false
		}
		return left > right
	})
	total := len(events)
	if len(events) > limit {
		events = events[:limit]
	}
	result, err := toolResult(map[string]any{"total": total, "events": events})
	if err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (s *Server) readRunEvents(runID string) []map[string]any {
	path := filepath.Join(s.cfg.RunsDir(), runID, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	events := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		event["run_id"] = runID
		events = append(events, event)
	}
	return events
}

func (s *Server) toolCIStatus(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	if err := control.ValidateRunID(args.RunID); err != nil {
		return nil, invalidParams(err)
	}
	run, err := pipeline.LoadRun(s.cfg, args.RunID)
	if err != nil {
		return toolError(err.Error()), nil
	}
	blockers := ""
	if run.CIFix != nil && strings.TrimSpace(run.CIFix.BlockersPath) != "" {
		if data, readErr := os.ReadFile(run.CIFix.BlockersPath); readErr == nil {
			blockers = string(data)
		}
	}
	if blockers == "" {
		if data, readErr := os.ReadFile(filepath.Join(s.cfg.RunsDir(), run.ID, "ci-blockers.md")); readErr == nil {
			blockers = string(data)
		}
	}
	humanFeedback := ""
	if data, readErr := os.ReadFile(filepath.Join(s.cfg.RunsDir(), run.ID, "feedback", "delivery.md")); readErr == nil {
		humanFeedback = string(data)
	}
	value, err := toolResult(map[string]any{
		"run_id":         run.ID,
		"status":         run.Status,
		"stage":          run.Stage,
		"pr_url":         run.PRURL,
		"ci_fix":         run.CIFix,
		"blockers":       blockers,
		"human_feedback": humanFeedback,
	})
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}

func (s *Server) toolCIDecision(raw json.RawMessage) (any, *rpcError) {
	var args struct {
		RunID    string `json:"run_id"`
		Decision string `json:"decision"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, invalidParams(err)
	}
	feedbackPath, err := control.SetDeliveryFeedback(s.cfg, args.RunID, args.Decision, args.Feedback)
	if err != nil {
		return toolError(err.Error()), nil
	}
	if _, active := control.DaemonActive(s.cfg, 15*time.Second); !active {
		return toolError("daemon não está ativo; inicie aiw daemon para processar a decisão"), nil
	}
	store, err := control.OpenOperationStore(s.cfg)
	if err != nil {
		return toolError(err.Error()), nil
	}
	operation, err := store.Enqueue(control.ActionResume, args.RunID, map[string]any{})
	if err != nil {
		return toolError(err.Error()), nil
	}
	value, err := toolResult(map[string]any{"operation": operation, "feedback_path": feedbackPath})
	if err != nil {
		return nil, invalidParams(err)
	}
	return value, nil
}
