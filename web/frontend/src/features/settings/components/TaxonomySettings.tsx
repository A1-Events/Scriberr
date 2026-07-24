import React, { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, Building2, Trash2, Check, Pencil, Plus, X } from "lucide-react";
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

/**
 * Companies & Projects (A1 fork): the taxonomy recordings are filed under.
 * A recording belongs to one company and carries any number of project tags.
 *
 * Renaming is always safe — recordings link by id, so renaming or re-scoping
 * never re-labels anything. Deleting a tag asks where its recordings should go.
 */
const TaxonomySettings: React.FC = () => {
  const { getAuthHeaders } = useAuth();
  const [companies, setCompanies] = useState<Company[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [editingCompanyId, setEditingCompanyId] = useState<number | null>(null);
  const [editingTagId, setEditingTagId] = useState<number | null>(null);
  const [editName, setEditName] = useState("");

  const [newCompany, setNewCompany] = useState({ key: "", name: "" });
  const [newTag, setNewTag] = useState<{ key: string; name: string; company_id: number | null }>({
    key: "",
    name: "",
    company_id: null,
  });

  // Tag pending deletion, with how many recordings it affects and where they go.
  const [deleting, setDeleting] = useState<{ tag: Tag; usage: number; reassignTo: number | null } | null>(null);

  const load = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const [cRes, tRes] = await Promise.all([
        fetch("/api/v1/companies", { headers: { ...getAuthHeaders() } }),
        fetch("/api/v1/tags", { headers: { ...getAuthHeaders() } }),
      ]);
      if (!cRes.ok) throw new Error(`Failed to load companies: ${cRes.statusText}`);
      if (!tRes.ok) throw new Error(`Failed to load projects: ${tRes.statusText}`);
      setCompanies(await cRes.json());
      setTags(await tRes.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load taxonomy");
    } finally {
      setIsLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    load();
  }, [load]);

  const send = async (url: string, method: string, body?: unknown) => {
    const res = await fetch(url, {
      method,
      headers: { ...getAuthHeaders(), "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) throw new Error((await res.text()) || res.statusText);
  };

  const run = async (fn: () => Promise<void>) => {
    try {
      await fn();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Operation failed");
    }
  };

  // Ask the server how many recordings a tag holds before offering to delete it.
  const startDelete = async (tag: Tag) => {
    try {
      const res = await fetch(`/api/v1/tags/${tag.id}/usage`, { headers: { ...getAuthHeaders() } });
      const usage = res.ok ? (await res.json()).recordings ?? 0 : 0;
      setDeleting({ tag, usage, reassignTo: null });
    } catch {
      setDeleting({ tag, usage: 0, reassignTo: null });
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    const q = deleting.reassignTo ? `?reassign_to=${deleting.reassignTo}` : "";
    await run(async () => {
      await send(`/api/v1/tags/${deleting.tag.id}${q}`, "DELETE");
      setDeleting(null);
    });
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-10">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }

  const tagsFor = (companyId: number | null) => tags.filter((t) => t.company_id === companyId);

  return (
    <div className="space-y-6">
      {error && <div className="text-sm text-red-500">{error}</div>}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Building2 className="h-4 w-4" /> Companies &amp; Projects
          </CardTitle>
          <CardDescription>
            Recordings are filed under one company and any number of projects. New recordings are
            classified automatically; correcting one teaches the classifier. Renaming is safe —
            nothing is re-labelled.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {companies.map((c) => (
            <div key={c.id} className="border rounded-lg p-3 space-y-2">
              <div className="flex items-center gap-2">
                {editingCompanyId === c.id ? (
                  <>
                    <Input value={editName} onChange={(e) => setEditName(e.target.value)} className="h-8" />
                    <Button
                      size="sm"
                      onClick={() =>
                        run(async () => {
                          await send(`/api/v1/companies/${c.id}`, "PUT", { name: editName });
                          setEditingCompanyId(null);
                        })
                      }
                    >
                      <Check className="h-4 w-4" />
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setEditingCompanyId(null)}>
                      <X className="h-4 w-4" />
                    </Button>
                  </>
                ) : (
                  <>
                    <span className="font-medium">{c.name}</span>
                    <span className="text-xs text-[var(--text-tertiary)]">{c.key}</span>
                    <div className="ml-auto flex gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setEditingCompanyId(c.id);
                          setEditName(c.name);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => run(() => send(`/api/v1/companies/${c.id}`, "DELETE"))}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </>
                )}
              </div>

              <div className="pl-4 space-y-1">
                {tagsFor(c.id).map((t) => (
                  <div key={t.id} className="flex items-center gap-2 text-sm">
                    {editingTagId === t.id ? (
                      <>
                        <Input value={editName} onChange={(e) => setEditName(e.target.value)} className="h-7" />
                        <Button
                          size="sm"
                          onClick={() =>
                            run(async () => {
                              await send(`/api/v1/tags/${t.id}`, "PUT", {
                                name: editName,
                                company_id: t.company_id,
                              });
                              setEditingTagId(null);
                            })
                          }
                        >
                          <Check className="h-4 w-4" />
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setEditingTagId(null)}>
                          <X className="h-4 w-4" />
                        </Button>
                      </>
                    ) : (
                      <>
                        <span>{t.name}</span>
                        <span className="text-xs text-[var(--text-tertiary)]">{t.key}</span>
                        <div className="ml-auto flex gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => {
                              setEditingTagId(t.id);
                              setEditName(t.name);
                            }}
                          >
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => startDelete(t)}>
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </>
                    )}
                  </div>
                ))}
                {tagsFor(c.id).length === 0 && (
                  <div className="text-xs text-[var(--text-tertiary)]">No projects yet.</div>
                )}
              </div>
            </div>
          ))}

          {tagsFor(null).length > 0 && (
            <div className="border rounded-lg p-3">
              <div className="font-medium mb-1">Any company</div>
              {tagsFor(null).map((t) => (
                <div key={t.id} className="flex items-center gap-2 text-sm">
                  <span>{t.name}</span>
                  <span className="text-xs text-[var(--text-tertiary)]">{t.key}</span>
                  <Button size="sm" variant="ghost" className="ml-auto" onClick={() => startDelete(t)}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex gap-2 items-center">
            <Input
              placeholder="COMPANY-KEY"
              value={newCompany.key}
              onChange={(e) => setNewCompany({ ...newCompany, key: e.target.value.toUpperCase() })}
              className="h-8 w-48"
            />
            <Input
              placeholder="Company name"
              value={newCompany.name}
              onChange={(e) => setNewCompany({ ...newCompany, name: e.target.value })}
              className="h-8"
            />
            <Button
              size="sm"
              disabled={!newCompany.key || !newCompany.name}
              onClick={() =>
                run(async () => {
                  await send("/api/v1/companies", "POST", newCompany);
                  setNewCompany({ key: "", name: "" });
                })
              }
            >
              <Plus className="h-4 w-4" /> Company
            </Button>
          </div>

          <div className="flex gap-2 items-center">
            <Input
              placeholder="PROJECT-KEY"
              value={newTag.key}
              onChange={(e) => setNewTag({ ...newTag, key: e.target.value.toUpperCase() })}
              className="h-8 w-48"
            />
            <Input
              placeholder="Project name"
              value={newTag.name}
              onChange={(e) => setNewTag({ ...newTag, name: e.target.value })}
              className="h-8"
            />
            <select
              className="h-8 rounded-md border bg-transparent px-2 text-sm"
              value={newTag.company_id ?? ""}
              onChange={(e) =>
                setNewTag({ ...newTag, company_id: e.target.value ? Number(e.target.value) : null })
              }
            >
              <option value="">Any company</option>
              {companies.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <Button
              size="sm"
              disabled={!newTag.key || !newTag.name}
              onClick={() =>
                run(async () => {
                  await send("/api/v1/tags", "POST", newTag);
                  setNewTag({ key: "", name: "", company_id: null });
                })
              }
            >
              <Plus className="h-4 w-4" /> Project
            </Button>
          </div>
        </CardContent>
      </Card>

      {deleting && (
        <Card className="border-red-500/40">
          <CardHeader>
            <CardTitle className="text-base">Delete “{deleting.tag.name}”?</CardTitle>
            <CardDescription>
              {deleting.usage === 0
                ? "No recordings use this project."
                : `${deleting.usage} recording${deleting.usage === 1 ? "" : "s"} use this project. ` +
                  "They keep their company; choose where the project label should go."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2 items-center">
            {deleting.usage > 0 && (
              <select
                className="h-8 rounded-md border bg-transparent px-2 text-sm"
                value={deleting.reassignTo ?? ""}
                onChange={(e) =>
                  setDeleting({
                    ...deleting,
                    reassignTo: e.target.value ? Number(e.target.value) : null,
                  })
                }
              >
                <option value="">Leave them with no project</option>
                {tags
                  // Only offer projects the affected recordings could legitimately
                  // carry: same company, or global. Offering another company's
                  // project would move them into a combination the recording UI
                  // and the classifier both treat as invalid.
                  .filter(
                    (t) =>
                      t.id !== deleting.tag.id &&
                      (t.company_id === null || t.company_id === deleting.tag.company_id),
                  )
                  .map((t) => (
                    <option key={t.id} value={t.id}>
                      Move to “{t.name}”
                    </option>
                  ))}
              </select>
            )}
            <Button size="sm" variant="destructive" onClick={confirmDelete}>
              Delete
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
};

export default TaxonomySettings;
