import { useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";


// Types
export interface MultiTrackFile {
    id: number;
    file_name: string;
    file_path: string;
    track_index: number;
}

export interface MultiTrackTiming {
    track_name: string;
    start_time: string;
    end_time: string;
    duration: number; // milliseconds
}

export interface ExecutionData {
    id?: string;
    transcription_job_id: string;
    started_at?: string;
    completed_at?: string | null;
    processing_duration?: number | null; // milliseconds
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    actual_parameters?: any;
    status?: string;
    error_message?: string | null;
    created_at?: string;
    updated_at?: string;
    // Multi-track specific fields
    is_multi_track?: boolean;
    multi_track_timings?: MultiTrackTiming[];
    merge_start_time?: string | null;
    merge_end_time?: string | null;
    merge_duration?: number | null; // milliseconds
    multi_track_files?: MultiTrackFile[];
    // Graceful empty response fields
    available?: boolean;
    message?: string;
}

export interface LogsData {
    job_id: string;
    available: boolean;
    content: string;
    message?: string;
}

export interface AudioFile {
    id: string;
    title?: string;
    status: "uploaded" | "pending" | "processing" | "completed" | "failed";
    created_at: string;
    updated_at?: string;
    audio_path: string;
    diarization?: boolean;
    is_multi_track?: boolean;
    multi_track_files?: MultiTrackFile[];
    merged_audio_path?: string;
    merge_status?: string;
    merge_error?: string;
    parameters?: {
        diarize?: boolean;
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        [key: string]: any;
    };
}

export interface WordSegment {
    start: number;
    end: number;
    word: string;
    score: number;
    speaker?: string;
}

export interface TranscriptSegment {
    start: number;
    end: number;
    text: string;
    speaker?: string;
}

export interface Transcript {
    text: string;
    segments?: TranscriptSegment[];
    word_segments?: WordSegment[];
}

export function useAudioDetail(audioId: string) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    const query = useQuery({
        queryKey: ["audio", audioId],
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${audioId}`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch audio details");
            return response.json() as Promise<AudioFile>;
        },
        // Poll while processing or pending
        refetchInterval: (query) => {
            const status = query.state.data?.status;
            if (status === "processing" || status === "pending") {
                return 3000;
            }
            return false;
        },
    });

    // Refresh everything derived from the transcript whenever the job produces a
    // NEW finished result. These endpoints return empty/`available:false`
    // payloads while a job is running, so they must not be invalidated at the
    // moment work is queued (e.g. on re-transcribe) — that would just cache the
    // empty response and leave it stale until a remount or window refocus.
    //
    // The trigger is the identity of the finished run (`status` + `updated_at`)
    // rather than an observed pending -> completed transition, because the page
    // can miss the interim states entirely: a short re-transcription can finish
    // between two 3s polls (completed -> completed), and retrying a failed job
    // that fails again immediately never leaves `failed`. Comparing the run
    // signature catches those; a stable signature never refires, so there is no
    // invalidation loop.
    const status = query.data?.status;
    const updatedAt = query.data?.updated_at;
    const finishedRun =
        status === "completed" || status === "failed" ? `${status}:${updatedAt ?? ""}` : undefined;

    const lastRefreshedRunRef = useRef<string | undefined>(undefined);
    const hasSeededRef = useRef(false);

    useEffect(() => {
        if (!audioId || !status) return;

        // The first result we see for this job is whatever the derived queries
        // already fetched on mount, so record it without refetching. (If the job
        // is still running on mount this seeds `undefined`, so the run that
        // finishes later is correctly treated as new.)
        if (!hasSeededRef.current) {
            hasSeededRef.current = true;
            lastRefreshedRunRef.current = finishedRun;
            return;
        }

        if (!finishedRun || lastRefreshedRunRef.current === finishedRun) return;
        lastRefreshedRunRef.current = finishedRun;

        queryClient.invalidateQueries({ queryKey: ["transcript", audioId] });
        queryClient.invalidateQueries({ queryKey: ["summary", audioId] });
        queryClient.invalidateQueries({ queryKey: ["executionData", audioId] });
        queryClient.invalidateQueries({ queryKey: ["logs", audioId] });
        queryClient.invalidateQueries({ queryKey: ["speakerMappings", audioId] });
    }, [status, finishedRun, audioId, queryClient]);

    return query;
}

export function useTranscript(audioId: string, enabled: boolean) {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: ["transcript", audioId],
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${audioId}/transcript`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch transcript");
            const data = await response.json();

            // Handle graceful empty responses (available=false)
            if (data.available === false || !data.transcript) {
                return null; // Return null to indicate no transcript
            }

            // Normalize transcript structure
            if (typeof data.transcript === "string") {
                return { text: data.transcript } as Transcript;
            } else if (data.transcript.text) {
                return {
                    text: data.transcript.text,
                    segments: data.transcript.segments,
                    word_segments: data.transcript.word_segments,
                } as Transcript;
            } else if (data.transcript.segments) {
                const fullText = data.transcript.segments
                    // eslint-disable-next-line @typescript-eslint/no-explicit-any
                    .map((segment: any) => segment.text)
                    .join(" ");
                return {
                    text: fullText,
                    segments: data.transcript.segments,
                    word_segments: data.transcript.word_segments,
                } as Transcript;
            }

            return { text: "" } as Transcript;
        },
        enabled: enabled,
    });
}

export function useExecutionData(audioId: string) {
    const { getAuthHeaders } = useAuth();
    return useQuery({
        queryKey: ["executionData", audioId],
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${audioId}/execution`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch execution data");
            return response.json() as Promise<ExecutionData>;
        },
        enabled: !!audioId,
    });
}

export function useLogs(audioId: string) {
    const { getAuthHeaders } = useAuth();
    return useQuery({
        queryKey: ["logs", audioId],
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${audioId}/logs`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch logs");
            return response.json() as Promise<LogsData>;
        },
        enabled: !!audioId,
    });
}

/**
 * Re-transcribe an existing recording, optionally overriding the language.
 *
 * The backend's POST /transcription/:id/start handler resets its parameters to
 * CPU/`small` defaults and only applies what the request body sends — it does NOT
 * merge with the job's stored params. So we must GET the job's current parameters
 * and resend the FULL params object with only `language` overridden, otherwise the
 * job silently downgrades off the GPU and to a smaller model.
 *
 * `language`: an ISO code (e.g. "ro"), or null to restore WhisperX auto-detect.
 */
export function useRetranscribe(audioId: string) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (language: string | null) => {
            // Re-fetch the job's current params so we resend them verbatim.
            const jobResponse = await fetch(`/api/v1/transcription/${audioId}`, {
                headers: getAuthHeaders(),
            });
            if (!jobResponse.ok) {
                throw new Error("Failed to load current transcription settings");
            }
            const job = (await jobResponse.json()) as AudioFile;

            // Full stored params, with only `language` overridden (null = auto-detect).
            const params = {
                ...(job.parameters || {}),
                language,
            };

            const response = await fetch(`/api/v1/transcription/${audioId}/start`, {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    ...getAuthHeaders(),
                },
                body: JSON.stringify(params),
            });
            if (!response.ok) {
                const msg = await response.text();
                throw new Error(msg || "Failed to start re-transcription");
            }
            return response.json();
        },
        onSuccess: () => {
            // Refresh the job record itself: the backend has already set the job
            // back to `pending`, so this kicks useAudioDetail's poll into gear.
            queryClient.invalidateQueries({ queryKey: ["audio", audioId] });
            queryClient.invalidateQueries({ queryKey: ["audioFiles"] }); // Update list too

            // Drop the previous run's outputs. The backend clears job.Transcript
            // before enqueueing, so leaving them cached would let the user read,
            // download or summarise a transcript that no longer exists while the
            // status already reflects the new run.
            //
            // `removeQueries` (not `invalidateQueries`): invalidating would mark
            // the empty in-flight response as the authoritative cached value with
            // nothing to supersede it, which is what previously left the UI stale
            // forever. Removing clears the entry outright; active observers then
            // show their loading/empty state, and the real results are fetched
            // when useAudioDetail sees the new finished run.
            queryClient.removeQueries({ queryKey: ["transcript", audioId] });
            queryClient.removeQueries({ queryKey: ["summary", audioId] });
            queryClient.removeQueries({ queryKey: ["executionData", audioId] });
            queryClient.removeQueries({ queryKey: ["logs", audioId] });
        },
    });
}

export function useUpdateTitle(audioId: string) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (newTitle: string) => {
            const response = await fetch(`/api/v1/transcription/${audioId}/title`, {
                method: "PUT",
                headers: {
                    "Content-Type": "application/json",
                    ...getAuthHeaders(),
                },
                body: JSON.stringify({ title: newTitle }),
            });
            if (!response.ok) {
                const msg = await response.text();
                throw new Error(msg || "Failed to update title");
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ["audio", audioId] });
            queryClient.invalidateQueries({ queryKey: ["audioFiles"] }); // Update list too
        },
    });
}
