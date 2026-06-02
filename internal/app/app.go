package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

type Context struct {
	Paths    config.Paths
	API      config.APIConfig
	Root     config.AgentConfig
	Sessions *session.Store
	Tools    *tools.Executor
	Agent    *llm.Agent
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

	store := session.NewStore(paths.WorkspaceSessionsDir())
	executor := tools.NewExecutor(paths, store, root.MaxOutputChars)
	agent := llm.NewAgent(api, root, paths, executor)
	var subSessions sync.Map
	executor.SetSubAgentRunner(func(parent context.Context, taskID, name, instruction string) string {
		subCfg, err := config.LoadAgent(paths, config.SubAgentKind, "default")
		if err != nil {
			return fmt.Sprintf("load sub-agent config: %v", err)
		}
		subStore := store
		var subSess *session.Session
		if loaded, ok := subSessions.Load(taskID); ok {
			subSess = loaded.(*session.Session)
		} else {
			subSess, err = subStore.New("sub-"+name, subCfg.Name)
			if err != nil {
				return fmt.Sprintf("create sub-agent session: %v", err)
			}
			subSessions.Store(taskID, subSess)
		}
		subExec := tools.NewReadOnlyExecutor(paths, subStore, subCfg.MaxOutputChars)
		sub := llm.NewSubAgent(api, subCfg, paths, subExec)
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		answer, err := sub.Ask(ctx, subSess, instruction)
		if saveErr := subStore.Save(subSess); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return fmt.Sprintf("sub-agent error: %v", err)
		}
		return answer
	})

	return &Context{
		Paths:    paths,
		API:      api,
		Root:     root,
		Sessions: store,
		Tools:    executor,
		Agent:    agent,
	}, nil
}
