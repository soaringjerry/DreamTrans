import type { StudyLessonDocument } from '../api'

interface LessonCardProps {
  skillLabel: string
  lesson: StudyLessonDocument
  /** Optional trailing action (e.g. "开始做题"). */
  action?: React.ReactNode
}

/**
 * 讲解卡: the one thing to remember, the terms that carry it, the traps,
 * and a worked example with its walkthrough. Read before the first item,
 * reopen from the practice stage any time.
 */
export function LessonCard({ skillLabel, lesson, action }: LessonCardProps) {
  return (
    <section aria-label={`讲解 ${skillLabel}`} className="dt-lesson">
      <header className="dt-lesson__head">
        <span className="st-label">讲解 // LESSON</span>
        <h3>{skillLabel}</h3>
      </header>

      <p className="dt-lesson__rule">{lesson.rule}</p>

      {lesson.concepts.length > 0 && (
        <div className="dt-lesson__block">
          <span className="st-label st-label--mu">关键术语</span>
          <dl className="dt-lesson__concepts">
            {lesson.concepts.map((concept) => (
              <div key={concept.term}>
                <dt>{concept.term}</dt>
                <dd>
                  {concept.gloss}
                  {concept.quote && <q>{concept.quote}</q>}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      )}

      {(lesson.misconceptions ?? []).length > 0 && (
        <div className="dt-lesson__block">
          <span className="st-label st-label--mu">常见误区</span>
          <ul className="dt-lesson__traps">
            {lesson.misconceptions!.map((trap) => (
              <li key={trap.label}>
                <code>{trap.label}</code>
                <span>{trap.how_to_tell}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="dt-lesson__block dt-lesson__example">
        <span className="st-label st-label--mu">例题 · 看看满分回答长什么样</span>
        <p className="dt-lesson__situation">{lesson.example.situation}</p>
        {lesson.example.question && <p className="dt-lesson__question">{lesson.example.question}</p>}
        <p className="dt-lesson__answer">{lesson.example.answer}</p>
        {lesson.example.walkthrough && <p className="dt-lesson__walkthrough">{lesson.example.walkthrough}</p>}
      </div>

      {action && <div className="dt-lesson__action">{action}</div>}
    </section>
  )
}
