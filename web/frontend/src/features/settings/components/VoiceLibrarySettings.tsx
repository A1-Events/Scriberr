import React, { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, Users, Trash2, Check, Pencil, GitMerge } from "lucide-react";
import { useAuth } from "@/features/auth/hooks/useAuth";

interface LibrarySpeaker {
  id: number;
  name: string;
  sample_count: number;
  created_at: string;
  updated_at: string;
}

/**
 * Voice Library (A1 fork): globally known voices learned from speaker renames.
 * People in this list are auto-recognized in future recordings.
 */
const VoiceLibrarySettings: React.FC = () => {
  const { getAuthHeaders } = useAuth();
  const [speakers, setSpeakers] = useState<LibrarySpeaker[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editName, setEditName] = useState("");
  const [mergeFromId, setMergeFromId] = useState<number | null>(null);

  const fetchSpeakers = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/voice-library", { headers: { ...getAuthHeaders() } });
      if (!res.ok) throw new Error(`Failed to load voice library: ${res.statusText}`);
      setSpeakers(await res.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load voice library");
    } finally {
      setIsLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    fetchSpeakers();
  }, [fetchSpeakers]);

  const rename = async (id: number) => {
    if (!editName.trim()) return;
    const res = await fetch(`/api/v1/voice-library/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      body: JSON.stringify({ name: editName.trim() }),
    });
    if (res.ok) {
      setEditingId(null);
      fetchSpeakers();
    } else {
      setError("Rename failed");
    }
  };

  const remove = async (id: number) => {
    const res = await fetch(`/api/v1/voice-library/${id}`, {
      method: "DELETE",
      headers: { ...getAuthHeaders() },
    });
    if (res.ok) fetchSpeakers();
    else setError("Delete failed");
  };

  const merge = async (toId: number) => {
    if (mergeFromId === null || mergeFromId === toId) return;
    const res = await fetch("/api/v1/voice-library/merge", {
      method: "POST",
      headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      body: JSON.stringify({ from_id: mergeFromId, to_id: toId }),
    });
    setMergeFromId(null);
    if (res.ok) fetchSpeakers();
    else setError("Merge failed");
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Users className="h-5 w-5" />
          Voice Library
        </CardTitle>
        <CardDescription>
          Voices learned from your speaker renames. Anyone listed here is
          automatically recognized in future recordings. Rename a speaker in any
          transcript to enroll them; more renames make recognition stronger.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error && (
          <p className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</p>
        )}
        {isLoading ? (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading…
          </div>
        ) : speakers.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            No voices enrolled yet — rename speakers in a diarized transcript to
            start building the library.
          </p>
        ) : (
          <div className="space-y-2">
            {mergeFromId !== null && (
              <p className="text-xs text-muted-foreground">
                Merging “{speakers.find((s) => s.id === mergeFromId)?.name}” — pick the
                voice to keep, or{" "}
                <button className="underline" onClick={() => setMergeFromId(null)}>
                  cancel
                </button>
                .
              </p>
            )}
            {speakers.map((s) => (
              <div
                key={s.id}
                className="flex items-center justify-between gap-2 rounded-lg border border-[var(--border-subtle)] px-3 py-2"
              >
                <div className="flex-1 min-w-0">
                  {editingId === s.id ? (
                    <Input
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && rename(s.id)}
                      autoFocus
                      className="h-8"
                    />
                  ) : (
                    <>
                      <span className="font-medium">{s.name}</span>
                      <span className="ml-2 text-xs text-muted-foreground">
                        {s.sample_count} voice sample{s.sample_count === 1 ? "" : "s"}
                      </span>
                    </>
                  )}
                </div>
                <div className="flex items-center gap-1">
                  {mergeFromId !== null && mergeFromId !== s.id ? (
                    <Button size="sm" variant="outline" onClick={() => merge(s.id)}>
                      <Check className="h-3.5 w-3.5 mr-1" /> Keep this
                    </Button>
                  ) : editingId === s.id ? (
                    <Button size="sm" variant="outline" onClick={() => rename(s.id)}>
                      <Check className="h-3.5 w-3.5" />
                    </Button>
                  ) : (
                    <>
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Rename"
                        onClick={() => {
                          setEditingId(s.id);
                          setEditName(s.name);
                        }}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Merge into another voice"
                        onClick={() => setMergeFromId(s.id)}
                      >
                        <GitMerge className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        title="Delete voice"
                        onClick={() => remove(s.id)}
                      >
                        <Trash2 className="h-3.5 w-3.5 text-red-500" />
                      </Button>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default VoiceLibrarySettings;
