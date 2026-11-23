import { mount } from 'svelte';
import App from './App.svelte';
import '@fontsource-variable/jetbrains-mono/wght.css';
import './app.css';

mount(App, { target: document.getElementById('app')! });
