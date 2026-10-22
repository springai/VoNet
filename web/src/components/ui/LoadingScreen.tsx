import { BrandLogo } from "../shell/BrandLogo";

export function LoadingScreen() {
  return (
    <div className="vonet-boot-loader" role="status" aria-live="polite" aria-label="Loading VoNet">
      <BrandLogo className="vonet-boot-logo" />
      <span className="sr-only">Loading VoNet</span>
    </div>
  );
}
