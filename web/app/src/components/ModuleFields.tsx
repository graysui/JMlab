export type ModuleField = { type: string; label: string; description?: string; secret?: boolean; required?: boolean; default?: unknown; options?: Array<{ value: string; label: string }> };
export function fieldInitial(field: ModuleField, value?: unknown): unknown {
  if (field.secret || field.type === "password") return "";
  const initial = value ?? field.default;
  if (field.type === "json") return initial == null ? "" : JSON.stringify(initial, null, 2);
  return initial ?? (field.type === "boolean" ? false : "");
}
export function fieldValue(field: ModuleField, value: unknown): unknown {
  if (field.type === "json") return typeof value === "string" && value.trim() ? JSON.parse(value) : null;
  if (field.type === "number") return value === "" ? undefined : Number(value);
  return value;
}
export function ModuleFieldInput({ field, value, onChange, required, disabled, placeholder }: { field: ModuleField; value: unknown; onChange: (value: unknown) => void; required?: boolean; disabled?: boolean; placeholder?: string }) {
  if (field.type === "boolean") return <input type="checkbox" disabled={disabled} checked={Boolean(value)} onChange={(e) => onChange(e.target.checked)} />;
  if (field.options?.length) return <select required={required} disabled={disabled} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)}><option value="">请选择</option>{field.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>;
  if (field.type === "json") return <textarea rows={4} required={required} disabled={disabled} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />;
  return <input type={field.secret || field.type === "password" ? "password" : field.type === "number" ? "number" : "text"} autoComplete={field.secret || field.type === "password" ? "new-password" : "off"} step={field.type === "number" ? "any" : undefined} required={required} disabled={disabled} placeholder={placeholder} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />;
}
