import App from './App'
import './ui-switcher.css'

export default function Root() {
  const goToPro = () => {
    window.location.href = '/pro'
  }

  return (
    <>
      <App />
      <button
        className="ui-switcher"
        type="button"
        onClick={goToPro}
      >
        <span className="ui-switcher__dot" />
        <span className="ui-switcher__label">试用 Pro UI</span>
      </button>
    </>
  )
}
