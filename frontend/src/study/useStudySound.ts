import { useCallback, useEffect, useState } from 'react'

/**
 * Synthesized electric-piano sound for the study stage. Everything is built
 * from oscillators (no audio files), so it works offline and under the
 * page's CSP. Two independent switches: SFX for feedback tones, BGM for a
 * quiet looping arpeggio. Both persist per browser.
 */

const STORAGE_KEY = 'dreamtrans.study.sound'

const NOTE: Record<string, number> = {
  C3: 130.81, D3: 146.83, E3: 164.81, F3: 174.61, G3: 196, A3: 220, B3: 246.94,
  C4: 261.63, D4: 293.66, E4: 329.63, F4: 349.23, G4: 392, A4: 440, B4: 493.88,
  C5: 523.25, D5: 587.33, E5: 659.25, F5: 698.46, G5: 783.99, A5: 880, B5: 987.77,
  C6: 1046.5, D6: 1174.66, E6: 1318.5, G6: 1567.98,
}

// Fmaj7 · Am7 · Dm7 · G7 as up-and-down arpeggios.
const BGM_CHORDS = [
  ['F3', 'A3', 'C4', 'E4', 'G4', 'E4', 'C4', 'A3'],
  ['A3', 'C4', 'E4', 'G4', 'B4', 'G4', 'E4', 'C4'],
  ['D3', 'F3', 'A3', 'C4', 'E4', 'C4', 'A3', 'F3'],
  ['G3', 'B3', 'D4', 'F4', 'A4', 'F4', 'D4', 'B3'],
]
const BGM_STEP_MS = 395

interface NoteOptions {
  dur?: number
  vel?: number
  pan?: number
  bright?: number
}

interface SoundPrefs {
  sfx: boolean
  bgm: boolean
}

function loadPrefs(): SoundPrefs {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<SoundPrefs>
      return {
        sfx: typeof parsed.sfx === 'boolean' ? parsed.sfx : true,
        bgm: typeof parsed.bgm === 'boolean' ? parsed.bgm : false,
      }
    }
  } catch {
    // Private mode or blocked storage: fall through to defaults.
  }
  return { sfx: true, bgm: false }
}

function savePrefs(prefs: SoundPrefs): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs))
  } catch {
    // Ignore: the switch still works for this page load.
  }
}

/** One synth shared by every hook instance on the page. */
class StudySynth {
  private ctx: AudioContext | null = null
  private master: GainNode | null = null
  private delay: DelayNode | null = null
  private bgmTimer = 0
  private bgmStep = 0
  prefs: SoundPrefs = loadPrefs()
  private listeners = new Set<() => void>()

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private emit(): void {
    this.listeners.forEach((listener) => listener())
  }

  setSfx(on: boolean): void {
    this.prefs = { ...this.prefs, sfx: on }
    savePrefs(this.prefs)
    this.emit()
    if (on) this.tick()
  }

  setBgm(on: boolean): void {
    this.prefs = { ...this.prefs, bgm: on }
    savePrefs(this.prefs)
    this.emit()
    window.clearTimeout(this.bgmTimer)
    this.bgmTimer = 0
    if (on && this.ensure()) this.bgmTick()
  }

  /** Browsers only start audio after a gesture; call from a click handler. */
  resume(): void {
    if (this.prefs.sfx || this.prefs.bgm) this.ensure()
    if (this.prefs.bgm && !this.bgmTimer && this.ctx) this.bgmTick()
  }

  private ensure(): boolean {
    if (!this.ctx) {
      const Ctor = window.AudioContext
        ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
      if (!Ctor) return false
      this.ctx = new Ctor()
      this.master = this.ctx.createGain()
      this.master.gain.value = 0.7
      this.master.connect(this.ctx.destination)
      this.delay = this.ctx.createDelay(1)
      this.delay.delayTime.value = 0.27
      const feedback = this.ctx.createGain()
      feedback.gain.value = 0.28
      const wet = this.ctx.createGain()
      wet.gain.value = 0.2
      const lowpass = this.ctx.createBiquadFilter()
      lowpass.type = 'lowpass'
      lowpass.frequency.value = 2400
      this.delay.connect(feedback)
      feedback.connect(lowpass)
      lowpass.connect(this.delay)
      this.delay.connect(wet)
      wet.connect(this.master)
    }
    if (this.ctx.state === 'suspended') void this.ctx.resume()
    return true
  }

  private note(freq: number, when: number, options: NoteOptions = {}): void {
    if (!this.ensure() || !this.ctx || !this.master || !this.delay) return
    const { dur = 1.3, vel = 0.35, pan = 0, bright = 1 } = options
    const ctx = this.ctx
    const t = ctx.currentTime + when
    const carrier = ctx.createOscillator()
    carrier.type = 'sine'
    carrier.frequency.value = freq
    const modulator = ctx.createOscillator()
    modulator.type = 'sine'
    modulator.frequency.value = freq
    const modGain = ctx.createGain()
    modGain.gain.setValueAtTime(freq * 1.6 * bright, t)
    modGain.gain.exponentialRampToValueAtTime(freq * 0.05, t + 0.35)
    modulator.connect(modGain)
    modGain.connect(carrier.frequency)
    const overtone = ctx.createOscillator()
    overtone.type = 'sine'
    overtone.frequency.value = freq * 2
    const overtoneGain = ctx.createGain()
    overtoneGain.gain.setValueAtTime(0.18, t)
    overtoneGain.gain.exponentialRampToValueAtTime(0.001, t + dur * 0.45)
    const envelope = ctx.createGain()
    envelope.gain.setValueAtTime(0.0001, t)
    envelope.gain.exponentialRampToValueAtTime(vel, t + 0.006)
    envelope.gain.exponentialRampToValueAtTime(0.0001, t + dur)
    const tremolo = ctx.createOscillator()
    tremolo.frequency.value = 4.5
    const tremoloGain = ctx.createGain()
    tremoloGain.gain.value = 0.06
    tremolo.connect(tremoloGain)
    tremoloGain.connect(envelope.gain)
    let out: AudioNode = envelope
    if (typeof ctx.createStereoPanner === 'function') {
      const panner = ctx.createStereoPanner()
      panner.pan.value = pan
      envelope.connect(panner)
      out = panner
    }
    carrier.connect(envelope)
    overtone.connect(overtoneGain)
    overtoneGain.connect(envelope)
    out.connect(this.master)
    out.connect(this.delay)
    for (const osc of [carrier, modulator, overtone, tremolo]) {
      osc.start(t)
      osc.stop(t + dur + 0.05)
    }
  }

  private seq(names: string[], gap: number, options: NoteOptions = {}): void {
    if (!this.prefs.sfx) return
    names.forEach((name, index) => {
      this.note(NOTE[name], index * gap, { pan: (index - names.length / 2) * 0.12, ...options })
    })
  }

  tick(): void { this.seq(['C6'], 0, { dur: 0.16, vel: 0.12 }) }
  submit(): void { this.seq(['E5', 'G5'], 0.07, { dur: 0.35, vel: 0.2 }) }
  pass(grade: string): void {
    const notes = grade === 'HD'
      ? ['C5', 'E5', 'G5', 'B5', 'D6']
      : grade === 'D' ? ['C5', 'E5', 'G5', 'C6'] : ['C5', 'E5', 'G5']
    this.seq(notes, 0.085, { dur: 1.2, vel: 0.32 })
  }
  /** A soft descending major third: never a minor "you failed" cue. */
  miss(): void { this.seq(['E5', 'C5'], 0.16, { dur: 0.9, vel: 0.22, bright: 0.6 }) }
  giveup(): void { this.seq(['A4'], 0, { dur: 0.8, vel: 0.18, bright: 0.5 }) }
  next(): void { this.seq(['F3'], 0, { dur: 0.5, vel: 0.12, bright: 0.4 }) }
  levelUp(): void { this.seq(['C5', 'G5', 'C6', 'E6', 'G6'], 0.07, { dur: 1.8, vel: 0.34 }) }
  report(): void { this.seq(['F3', 'A3', 'C4', 'E4', 'G4', 'C5'], 0.11, { dur: 2.2, vel: 0.26 }) }

  private bgmTick(): void {
    if (!this.prefs.bgm) { this.bgmTimer = 0; return }
    const chord = BGM_CHORDS[Math.floor(this.bgmStep / 8) % BGM_CHORDS.length]
    const name = chord[this.bgmStep % 8]
    this.note(NOTE[name], 0, { dur: 1.7, vel: 0.075, bright: 0.5, pan: ((this.bgmStep % 8) - 4) * 0.08 })
    if (this.bgmStep % 16 === 15) this.note(NOTE.C6, 0.1, { dur: 2, vel: 0.05, bright: 0.4 })
    this.bgmStep += 1
    this.bgmTimer = window.setTimeout(() => this.bgmTick(), BGM_STEP_MS)
  }
}

let synth: StudySynth | null = null

function getSynth(): StudySynth {
  if (!synth) synth = new StudySynth()
  return synth
}

export interface StudySound {
  sfx: boolean
  bgm: boolean
  setSfx: (on: boolean) => void
  setBgm: (on: boolean) => void
  /** Call from any click handler so the browser lets audio start. */
  resume: () => void
  tick: () => void
  submit: () => void
  pass: (grade: string) => void
  miss: () => void
  giveup: () => void
  next: () => void
  levelUp: () => void
  report: () => void
}

export function useStudySound(): StudySound {
  const [, force] = useState(0)
  useEffect(() => getSynth().subscribe(() => force((value) => value + 1)), [])
  const s = getSynth()
  return {
    sfx: s.prefs.sfx,
    bgm: s.prefs.bgm,
    setSfx: useCallback((on: boolean) => getSynth().setSfx(on), []),
    setBgm: useCallback((on: boolean) => getSynth().setBgm(on), []),
    resume: useCallback(() => getSynth().resume(), []),
    tick: useCallback(() => getSynth().tick(), []),
    submit: useCallback(() => getSynth().submit(), []),
    pass: useCallback((grade: string) => getSynth().pass(grade), []),
    miss: useCallback(() => getSynth().miss(), []),
    giveup: useCallback(() => getSynth().giveup(), []),
    next: useCallback(() => getSynth().next(), []),
    levelUp: useCallback(() => getSynth().levelUp(), []),
    report: useCallback(() => getSynth().report(), []),
  }
}
