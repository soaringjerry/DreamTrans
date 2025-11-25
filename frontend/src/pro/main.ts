/**
 * DreamTrans Pro - Standalone Entry Point
 *
 * This is a completely independent entry point for the Pro version.
 * It does NOT load the Classic React app and uses cloud storage instead of IndexedDB.
 */
import { createApp } from 'vue'
import ProStandalone from './ProStandalone.vue'
import './pro-standalone.css'

const app = createApp(ProStandalone)
app.mount('#pro-root')
