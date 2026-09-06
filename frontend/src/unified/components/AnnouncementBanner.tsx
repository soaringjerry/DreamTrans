import type { Announcement } from '../../api'
import { useMessages } from '../../i18n'
import { Icon } from './Icon'

interface AnnouncementBannerProps {
  announcements: readonly Announcement[]
  onDismiss: (id: string) => void
}

/**
 * Site notices written in the admin console. Each one stays until the reader
 * closes it; a signed-in dismissal follows the account across devices.
 */
export function AnnouncementBanner({ announcements, onDismiss }: AnnouncementBannerProps) {
  const m = useMessages().announcements
  if (announcements.length === 0) return null
  return (
    <div className="dt-announcements">
      {announcements.map((item) => (
        <div className={`dt-announcement dt-announcement--${item.level}`} key={item.id} role="status">
          <span className="dt-announcement__icon" aria-hidden="true">
            <Icon name={item.level === 'warning' ? 'shield' : item.level === 'success' ? 'check' : 'sparkles'} size={16} />
          </span>
          <div className="dt-announcement__body">
            <strong>{item.title}</strong>
            {item.body && <p>{item.body}</p>}
            {item.link_url && (
              <a href={item.link_url} rel="noreferrer" target={item.link_url.startsWith('/') ? undefined : '_blank'}>
                {item.link_label || m.open}
              </a>
            )}
          </div>
          <button aria-label={m.dismiss} className="dt-icon-button" onClick={() => onDismiss(item.id)} type="button">
            <Icon name="close" size={16} />
          </button>
        </div>
      ))}
    </div>
  )
}
