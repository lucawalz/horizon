export function fieldValue(form: FormData, field: string): string {
  const held = form.get(field)
  return typeof held === 'string' ? held.trim() : ''
}

export function numberValue(form: FormData, field: string): number {
  return Number(fieldValue(form, field))
}
