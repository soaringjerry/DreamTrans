/**
 * DreamTrans Pro - Admin Panel Entry Point
 */
import { createApp } from 'vue'
import ProAdmin from './ProAdmin.vue'
import './pro-standalone.css'

const app = createApp(ProAdmin)
app.mount('#pro-admin-root')
