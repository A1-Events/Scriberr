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

    return useQuery({
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
            // Job is now pending → useAudioDetail's poll picks up live status/progress.
            // Clear stale results so the UI refreshes once the new run completes.
            queryClient.invalidateQueries({ queryKey: ["audio", audioId] });
            queryClient.invalidateQueries({ queryKey: ["transcript", audioId] });
            queryClient.invalidateQueries({ queryKey: ["executionData", audioId] });
            queryClient.invalidateQueries({ queryKey: ["logs", audioId] });
            queryClient.invalidateQueries({ queryKey: ["summary", audioId] });
            queryClient.invalidateQueries({ queryKey: ["audioFiles"] }); // Update list too
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
