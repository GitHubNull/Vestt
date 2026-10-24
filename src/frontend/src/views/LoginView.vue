<script setup lang="ts">
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

// Upstream login.html parity: a single admin-password field posting to
// /UI/Dashboard. Failure bounces back to the login page (upstream shows no
// error message), so a wrong password simply keeps the user here.
const password = ref('')
const submitting = ref(false)
const auth = useAuthStore()
const router = useRouter()

async function submit() {
  submitting.value = true
  try {
    const ok = await auth.login(password.value)
    if (ok) {
      router.push({ name: 'dashboard' })
    }
    password.value = ''
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <div class="login-card">
      <h1 class="login-title">Vestt</h1>
      <h2 class="login-subtitle">Login</h2>
      <form class="login-form" @submit.prevent="submit">
        <label class="login-label" for="admin-password">Admin password</label>
        <Password
          id="admin-password"
          v-model="password"
          :feedback="false"
          toggle-mask
          fluid
          :input-props="{ autocomplete: 'current-password' }"
          @keyup.enter="submit"
        />
        <InputText v-show="false" type="text" name="username" autocomplete="username" />
        <Button type="submit" label="Login" :loading="submitting" />
      </form>
    </div>
  </main>
</template>

<style scoped>
.login-shell {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--p-surface-100);
}
.login-card {
  background: var(--p-surface-0);
  border-radius: var(--p-border-radius-lg);
  box-shadow: var(--p-shadow-lg);
  padding: 2.5rem 3rem;
  min-width: 22rem;
}
.login-title {
  margin: 0;
  font-size: 1.75rem;
}
.login-subtitle {
  margin: 0.25rem 0 1.5rem;
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--p-text-muted-color);
}
.login-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.login-label {
  font-weight: 600;
}
</style>
