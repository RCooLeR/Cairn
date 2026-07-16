import { useCallback, useLayoutEffect, useRef, useState } from "react";

const transcriptBottomThreshold = 56;

export function useTranscriptAutoFollow(
  contentRevision: unknown,
  hasContent: boolean,
) {
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const shouldFollowRef = useRef(true);
  const [hasUnseenContent, setHasUnseenContent] = useState(false);

  const jumpToLatest = useCallback(() => {
    const transcript = transcriptRef.current;
    if (transcript) {
      transcript.scrollTop = transcript.scrollHeight;
    }
    shouldFollowRef.current = true;
    setHasUnseenContent(false);
  }, []);

  const handleScroll = useCallback(() => {
    const transcript = transcriptRef.current;
    if (!transcript) {
      return;
    }
    const distanceFromBottom =
      transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight;
    const isNearBottom = distanceFromBottom <= transcriptBottomThreshold;
    shouldFollowRef.current = isNearBottom;
    if (isNearBottom) {
      setHasUnseenContent(false);
    }
  }, []);

  useLayoutEffect(() => {
    const transcript = transcriptRef.current;
    if (!transcript) {
      return;
    }
    if (!hasContent) {
      transcript.scrollTop = 0;
      shouldFollowRef.current = true;
      setHasUnseenContent(false);
      return;
    }
    if (shouldFollowRef.current) {
      transcript.scrollTop = transcript.scrollHeight;
      setHasUnseenContent(false);
      return;
    }
    setHasUnseenContent(true);
  }, [contentRevision, hasContent]);

  return {
    handleScroll,
    hasUnseenContent,
    jumpToLatest,
    transcriptRef,
  };
}
