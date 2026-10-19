# Gallery video fixture

`clip.mp4` is a synthetic one-second, 32 by 32 pixel blue H.264 video. It
exercises file pinning and acceptance records; it cannot verify the accuracy
of a person's visual judgment. Tests read the fixture with the Go standard
library and do not require a media encoder.

It was generated with:

```sh
ffmpeg -f lavfi -i 'color=c=blue:s=32x32:r=2:d=1' -an -c:v libx264 \
  -pix_fmt yuv420p -movflags +faststart clip.mp4
```
