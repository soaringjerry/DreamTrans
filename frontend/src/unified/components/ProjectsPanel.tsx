import { useState, type FormEvent } from 'react'
import type { AIProjectsState } from '../hooks/useAIProjects'
import { Icon } from './Icon'

interface ProjectsPanelProps {
  state: AIProjectsState
  /** '' when no session is open. */
  activeSessionId: string
  /** False for local-only sessions, which have no server row to link. */
  sessionLinkable: boolean
}

/**
 * Sidebar project management: create/rename/delete projects and toggle the
 * current session's project link without opening the AI panel. Clicking a
 * project links the open session to it (a session belongs to at most one
 * project, so this moves it); clicking the linked project unlinks.
 */
export function ProjectsPanel({
  state,
  activeSessionId,
  sessionLinkable,
}: ProjectsPanelProps) {
  const [adding, setAdding] = useState(false)
  const [draftName, setDraftName] = useState('')

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (state.busy) return
    if (await state.create(draftName)) {
      setDraftName('')
      setAdding(false)
    }
  }

  const toggleLink = (projectId: string) => {
    if (state.busy) return
    if (projectId === state.linkedProjectId) {
      void state.unlinkCurrentSession()
    } else {
      void state.linkCurrentSession(projectId)
    }
  }

  const renameProject = (projectId: string, currentName: string) => {
    const next = window.prompt('项目名称', currentName)
    if (next === null) return
    void state.rename(projectId, next)
  }

  const removeProject = (projectId: string, name: string) => {
    if (!window.confirm(
      `删除项目“${name || '未命名项目'}”？项目里的资料会一并删除，会话本身不受影响。`,
    )) return
    void state.remove(projectId)
  }

  const linkHint = !activeSessionId
    ? '先打开一个会话，再点击项目进行关联'
    : !sessionLinkable
      ? '本地会话无法关联项目，请先上传到云端'
      : undefined

  return (
    <div className="dt-projects" aria-label="项目">
      <div className="dt-sidebar__history-heading dt-projects__heading">
        <span>项目</span>
        <button
          aria-label={adding ? '取消新建项目' : '新建项目'}
          className="dt-icon-button"
          onClick={() => {
            setAdding((value) => !value)
            setDraftName('')
          }}
          type="button"
        >
          <Icon name={adding ? 'close' : 'plus'} size={16} />
        </button>
      </div>

      {adding && (
        <form className="dt-projects__create" onSubmit={(event) => { void submitCreate(event) }}>
          <input
            autoFocus
            maxLength={160}
            onChange={(event) => setDraftName(event.target.value)}
            placeholder="项目名称"
            value={draftName}
          />
          <button
            className="dt-icon-button"
            disabled={state.busy || !draftName.trim()}
            aria-label="创建项目"
            type="submit"
          >
            <Icon name="check" size={16} />
          </button>
        </form>
      )}

      {state.error && (
        <p className="dt-projects__error" role="alert">
          {state.error}
          <button aria-label="关闭错误提示" onClick={state.clearError} type="button">
            <Icon name="close" size={12} />
          </button>
        </p>
      )}

      <div className="dt-projects__list">
        {state.loading && state.projects.length === 0 && (
          <p className="dt-projects__empty">正在加载项目…</p>
        )}
        {!state.loading && state.projects.length === 0 && !adding && (
          <p className="dt-projects__empty">还没有项目。建一个，把相关会话和资料挂在一起。</p>
        )}
        {state.projects.map((project) => {
          const linked = project.id === state.linkedProjectId
          return (
            <div
              className={`dt-projects__item${linked ? ' is-linked' : ''}`}
              key={project.id}
            >
              <button
                className="dt-projects__main"
                disabled={state.busy || !activeSessionId || !sessionLinkable}
                onClick={() => toggleLink(project.id)}
                title={linkHint ?? (linked ? '点击取消当前会话与此项目的关联' : '点击把当前会话关联到此项目')}
                type="button"
              >
                <span className="dt-projects__mark" aria-hidden>
                  {linked ? <Icon name="check" size={13} /> : <Icon name="paperclip" size={13} />}
                </span>
                <span className="dt-projects__name">{project.name || '未命名项目'}</span>
                {linked && <small>当前会话</small>}
              </button>
              <span className="dt-projects__actions">
                <button
                  aria-label={`重命名 ${project.name}`}
                  className="dt-icon-button"
                  disabled={state.busy}
                  onClick={() => renameProject(project.id, project.name)}
                  title="重命名"
                  type="button"
                >
                  <Icon name="settings" size={14} />
                </button>
                <button
                  aria-label={`删除 ${project.name}`}
                  className="dt-icon-button dt-icon-button--danger"
                  disabled={state.busy}
                  onClick={() => removeProject(project.id, project.name)}
                  title="删除项目"
                  type="button"
                >
                  <Icon name="close" size={14} />
                </button>
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
