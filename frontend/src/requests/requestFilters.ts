const fields = ['reference', 'client_identity', 'route', 'source', 'error_classification', 'operation', 'status', 'cache', 'credential', 'window', 'sort', 'limit'] as const

export function requestQueryFromForm(form: FormData) {
  const query = new URLSearchParams()
  for (const field of fields) {
    const value = String(form.get(field) ?? '').trim()
    if (value) query.set(field, value)
  }
  return query
}
