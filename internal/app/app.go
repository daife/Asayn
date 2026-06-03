package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/llm/usage"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

type Context struct {
	Paths        config.Paths
	API          config.APIConfig
	Root         config.AgentConfig
	Sessions     *session.Store
	SubSessions  *session.Store
	Tools        *tools.Executor
	Agent        *llm.Agent
	UsageTracker *usage.Tracker
}

func Bootstrap(cwd string) (*Context, error) {
	paths, err := config.Bootstrap(cwd)
	if err != nil {
		return nil, err
	}

	api, err := config.LoadAPIConfig(paths)
	if err != nil {
		return nil, err
	}

	root, err := config.LoadAgent(paths, config.RootAgentKind, "default")
	if err != nil {
		return nil, err
	}

	store := session.NewStore(paths.RootAgentSessionsDir())
	subStore := session.NewStore(paths.SubAgentSessionsDir())
	executor := tools.NewExecutor(paths, store, root.MaxOutputLines, root.AllowParallelShell, root.AllowInteractiveShell)
	agent := llm.NewAgent(api, root, paths, executor)
	usageTracker := usage.NewTracker(paths)

	var subSessions sync.Map
	executor.SetSubAgentRunner(func(parent context.Context, parentSessionID, taskID, sessionID, agentName, name, instruction string, emit func(string), bind func(string)) string {
		if agentName == "" {
			agentName = "default"
		}
		subCfg, err := config.LoadAgent(paths, config.SubAgentKind, agentName)
		if err != nil {
			return fmt.Sprintf("load sub-agent config: %v", err)
		}
		var subSess *session.Session
		if loaded, ok := subSessions.Load(taskID); ok {
			subSess = loaded.(*session.Session)
		} else if sessionID != "" {
			subSess, err = subStore.LoadByID(sessionID)
			if err != nil {
				return fmt.Sprintf("load sub-agent session: %v", err)
			}
			subSessions.Store(taskID, subSess)
		} else {
			subSess, err = subStore.New("sub-"+name, subCfg.Name)
			if err != nil {
				return fmt.Sprintf("create sub-agent session: %v", err)
			}
			subSessions.Store(taskID, subSess)
		}
		if bind != nil {
			bind(subSess.ID)
		}
		subExec := tools.NewReadOnlyExecutor(paths, subStore, subCfg.MaxOutputLines)
		sub := llm.NewSubAgent(api, subCfg, paths, subExec)
		sub.RefreshSystemPrompt(subSess)
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		answer, use, err := sub.AskWithEvents(ctx, subSess, instruction, nil)
		if err == nil {
			_ = usageTracker.Log(parentSessionID, subSess.Name, subCfg.Model, use)
		}
		if saveErr := subStore.Save(subSess); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return fmt.Sprintf("sub-agent error: %v", err)
		}
		return answer
	})

	return &Context{
		Paths:        paths,
		API:          api,
		Root:         root,
		Sessions:     store,
		SubSessions:  subStore,
		Tools:        executor,
		Agent:        agent,
		UsageTracker: usageTracker,
	}, nil
}
