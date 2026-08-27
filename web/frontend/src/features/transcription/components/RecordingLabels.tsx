import React, { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Check, Loader2, Pencil, Sparkles } from "lucide-react";
import { useAuth } from "@/features/auth/hooks/useAuth";

interface Company {
  id: number;
  key: string;
  name: string;
}
interface Tag {
  id: number;
  key: string;
  name: string;
  company_id: number | null;
}
interface JobTag {
  tag_id: number;
  source: string;
  tag?: Tag;
}

/**
 * Company + project labels for one recording (A1 fork).
 *
 * Whatever is saved here is recorded as a human correction, and corrections are
 * what the classifier learns from — so fixing a wrong label here improves the
 * labelling of future recordings.
 */
const RecordingLabels: React.FC<{ jobId: string }> = ({ jobId }) => {
  const { getAuthHeaders } = useAuth();
  const [companies, setCompanies] = useState<Company[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [companyId, setCompanyId] = useState<number | null>(null);
  const [tagIds, setTagIds] = useState<number[]>([]);
  const [wasAuto, setWasAuto] = useState(false);
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [available, setAvailable] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [cRes, tRes, lRes] = await Promise.all([
        fetch("/api/v1/companies", { headers: { ...getAuthHeaders() } }),
        fetch("/api/v1/tags", { headers: { ...getAuthHeaders() } }),
        fetch(`/api/v1/transcription/${jobId}/labels`, { headers: { ...getAuthHeaders() } }),
      ]);
      // Tagging is optional server-side; hide the control entirely if disabled.
      if (cRes.status === 404 || tRes.status === 404) {
        setAvailable(false);
        return;
      }
      if (cRes.ok) setCompanies(await cRes.json());
      if (tRes.ok) setTags(await tRes.json());
      if (lRes.ok) {
        const data = await lRes.json();
        setCompanyId(data.company_id ?? null);
        const links: JobTag[] = data.tags ?? [];
        setTagIds(links.map((l) => l.tag_id));
        setWasAuto(data.company_source === "auto" || links.some((l) => l.source === "auto"));
      }
    } catch {
      setAvailable(false);
    }
  }, [jobId, getAuthHeaders]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await fetch(`/api/v1/transcription/${jobId}/labels`, {
        method: "PUT",
        headers: { ...getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ company_id: companyId, tag_ids: tagIds }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(body.error || "Could not save labels");
      }
      setEditing(false);
      setWasAuto(false);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save labels");
    } finally {
      setBusy(false);
    }
  };

  if (!available || companies.length === 0) return null;

  const company = companies.find((c) => c.id === companyId);
  const selectable = tags.filter((t) => t.company_id === null || t.company_id === companyId);
  const chosen = tags.filter((t) => tagIds.includes(t.id));

  if (!editing) {
    return (
      <div className="flex items-center gap-2 flex-wrap text-xs">
        <span className="px-2 py-0.5 rounded-full bg-[var(--bg-card)] border font-medium">
          {company ? company.name : "Unclassified"}
        </span>
        {chosen.map((t) => (
          <span key={t.id} className="px-2 py-0.5 rounded-full bg-[var(--bg-card)] border">
            {t.name}
          </span>
        ))}
        {wasAuto && (
          <span
            className="flex items-center gap-1 text-[var(--text-tertiary)]"
            title="Suggested automatically — correct it to teach the classifier"
          >
            <Sparkles className="h-3 w-3" /> auto
          </span>
        )}
        <Button size="sm" variant="ghost" className="h-6 px-1" onClick={() => setEditing(true)}>
          <Pencil className="h-3 w-3" />
        </Button>
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2 flex-wrap text-xs">
      <select
        className="h-7 rounded-md border bg-transparent px-2"
        value={companyId ?? ""}
        onChange={(e) => {
          const next = e.target.value ? Number(e.target.value) : null;
          setCompanyId(next);
          // Drop tags that don't belong to the newly chosen company.
          setTagIds((prev) =>
            prev.filter((id) => {
              const t = tags.find((x) => x.id === id);
              return t && (t.company_id === null || t.company_id === next);
            }),
          );
        }}
      >
        <option value="">Unclassified</option>
        {companies.map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
          </option>
        ))}
      </select>

      {selectable.map((t) => {
        const on = tagIds.includes(t.id);
        return (
          <button
            key={t.id}
            type="button"
            onClick={() => setTagIds((prev) => (on ? prev.filter((x) => x !== t.id) : [...prev, t.id]))}
            className={`px-2 py-0.5 rounded-full border transition-colors ${
              on ? "bg-[var(--text-primary)] text-[var(--bg-card)]" : "bg-transparent"
            }`}
          >
            {t.name}
          </button>
        );
      })}

      <Button size="sm" className="h-6 px-2" onClick={save} disabled={busy}>
        {busy ? <Loader2 className="h-3 w-3 animate-spin" /> : <Check className="h-3 w-3" />}
      </Button>
      <Button size="sm" variant="ghost" className="h-6 px-2" onClick={() => { setEditing(false); load(); }}>
        Cancel
      </Button>
      {error && <span className="text-red-500">{error}</span>}
    </div>
  );
};

export default RecordingLabels;
