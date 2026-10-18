package org.saturday.emulator;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ScrollView;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ImageView;
import android.graphics.BitmapFactory;
import android.widget.TextView;

import androidx.documentfile.provider.DocumentFile;

import org.saturday.emulator.mobile.EbitenView;
import org.saturday.emulator.mobile.Mobile;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.ArrayList;
import java.util.List;
import java.util.HashMap;
import java.util.Map;

public final class MainActivity extends Activity {
    private static final int REQUEST_GAME_FOLDER = 101;
    private static final int REQUEST_BIOS = 102;
    private static final String BOXART_INDEX = "https://thumbnails.libretro.com/Sega%20-%20Saturn/Named_Boxarts/";

    private EbitenView ebitenView;
    private TextView statusView;
    private LinearLayout controlPanel;
    private Button menuButton;
    private LinearLayout gameListContainer;
    private LinearLayout recentListContainer;
    private LinearLayout gameActions;
    private EditText searchInput;
    private final List<File> libraryDiscs = new ArrayList<>();
    private final Map<String, File> libraryCovers = new HashMap<>();
    private File gameDirectory;
    private File currentGame;
    private boolean gameRunning;
    private final Handler uiHandler = new Handler(Looper.getMainLooper());
    private final Runnable diagnosticsUpdater = new Runnable() {
        @Override public void run() {
            if (gameRunning && statusView != null && controlPanel != null
                    && controlPanel.getVisibility() == View.VISIBLE) {
                statusView.setText(Mobile.getDiagnostics());
                uiHandler.postDelayed(this, 1000);
            }
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        Mobile.setBackupRAMPath(new File(getFilesDir(), "saturn-backup.ram").getAbsolutePath());
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        getWindow().setFlags(WindowManager.LayoutParams.FLAG_FULLSCREEN,
                WindowManager.LayoutParams.FLAG_FULLSCREEN);
        hideSystemUi();

        FrameLayout root = new FrameLayout(this);
        ebitenView = new EbitenView(this);
        root.addView(ebitenView, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

        controlPanel = new LinearLayout(this);
        controlPanel.setOrientation(LinearLayout.VERTICAL);
        controlPanel.setPadding(dp(14), dp(10), dp(14), dp(10));
        controlPanel.setBackgroundColor(0xEE111827);

        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        Button gamesButton = new Button(this);
        gamesButton.setText("Choose games");
        gamesButton.setAllCaps(false);
        gamesButton.setOnClickListener(v -> chooseGameFolder());
        actions.addView(gamesButton, new LinearLayout.LayoutParams(0, dp(48), 1));

        Button biosButton = new Button(this);
        biosButton.setText("BIOS (optional)");
        biosButton.setAllCaps(false);
        biosButton.setOnClickListener(v -> chooseBios());
        actions.addView(biosButton, new LinearLayout.LayoutParams(0, dp(48), 1));
        controlPanel.addView(actions);

        gameActions = new LinearLayout(this);
        gameActions.setOrientation(LinearLayout.HORIZONTAL);
        Button settingsButton = new Button(this);
        settingsButton.setText("Settings");
        settingsButton.setAllCaps(false);
        settingsButton.setOnClickListener(v -> showInGameSettings());
        gameActions.addView(settingsButton, new LinearLayout.LayoutParams(0, dp(44), 1));

        Button swapButton = new Button(this);
        swapButton.setText("Swap disc");
        swapButton.setAllCaps(false);
        swapButton.setOnClickListener(v -> showDiscSwapDialog());
        gameActions.addView(swapButton, new LinearLayout.LayoutParams(0, dp(44), 1));

        Button quitButton = new Button(this);
        quitButton.setText("Quit");
        quitButton.setAllCaps(false);
        quitButton.setOnClickListener(v -> confirmQuitGame());
        gameActions.addView(quitButton, new LinearLayout.LayoutParams(0, dp(44), 1));
        gameActions.setVisibility(View.GONE);
        controlPanel.addView(gameActions);

        TextView libraryTitle = new TextView(this);
        libraryTitle.setText("GAME LIBRARY");
        libraryTitle.setTextColor(0xFF93C5FD);
        libraryTitle.setTextSize(12);
        libraryTitle.setPadding(dp(4), dp(10), dp(4), dp(4));
        controlPanel.addView(libraryTitle);

        searchInput = new EditText(this);
        searchInput.setSingleLine(true);
        searchInput.setTextSize(14);
        searchInput.setTextColor(0xFFFFFFFF);
        searchInput.setHintTextColor(0xFF9CA3AF);
        searchInput.setHint("Search your Saturn games");
        searchInput.setPadding(dp(10), 0, dp(10), 0);
        controlPanel.addView(searchInput, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(44)));

        TextView recentTitle = new TextView(this);
        recentTitle.setText("RECENTLY PLAYED");
        recentTitle.setTextColor(0xFF93C5FD);
        recentTitle.setTextSize(12);
        recentTitle.setPadding(dp(4), dp(8), dp(4), dp(4));
        controlPanel.addView(recentTitle);
        recentListContainer = new LinearLayout(this);
        recentListContainer.setOrientation(LinearLayout.VERTICAL);
        controlPanel.addView(recentListContainer);

        ScrollView libraryScroll = new ScrollView(this);
        libraryScroll.setFillViewport(false);
        gameListContainer = new LinearLayout(this);
        gameListContainer.setOrientation(LinearLayout.VERTICAL);
        libraryScroll.addView(gameListContainer);
        LinearLayout.LayoutParams libraryParams = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(190));
        libraryParams.topMargin = dp(4);
        controlPanel.addView(libraryScroll, libraryParams);
        searchInput.addTextChangedListener(new android.text.TextWatcher() {
            @Override public void beforeTextChanged(CharSequence s, int start, int count, int after) {}
            @Override public void onTextChanged(CharSequence s, int start, int before, int count) { renderGameLibrary(); }
            @Override public void afterTextChanged(android.text.Editable s) {}
        });

        statusView = new TextView(this);
        statusView.setTextColor(0xFFFFFFFF);
        statusView.setTextSize(13);
        statusView.setLineSpacing(dp(3), 1.0f);
        statusView.setPadding(dp(4), dp(4), dp(4), dp(2));
        statusView.setText("SATURDAY · SEGA SATURN\n\nBuilt-in HLE BIOS is enabled automatically. An external BIOS is optional.\n\nChoose a folder containing CHD or CUE/BIN files.");
        controlPanel.addView(statusView);

        FrameLayout.LayoutParams panelParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.WRAP_CONTENT);
        panelParams.gravity = android.view.Gravity.TOP;
        root.addView(controlPanel, panelParams);

        menuButton = new Button(this);
        menuButton.setText("MENU");
        menuButton.setAllCaps(false);
        menuButton.setTextSize(12);
        menuButton.setVisibility(View.GONE);
        menuButton.setOnClickListener(v -> {
            boolean show = controlPanel.getVisibility() != View.VISIBLE;
            controlPanel.setVisibility(show ? View.VISIBLE : View.GONE);
            menuButton.setText(show ? "HIDE" : "MENU");
            if (show && gameRunning) {
                statusView.setText(Mobile.getDiagnostics());
                uiHandler.removeCallbacks(diagnosticsUpdater);
                uiHandler.postDelayed(diagnosticsUpdater, 1000);
            } else {
                uiHandler.removeCallbacks(diagnosticsUpdater);
            }
        });
        FrameLayout.LayoutParams menuParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT, dp(44));
        menuParams.gravity = android.view.Gravity.TOP | android.view.Gravity.END;
        menuParams.setMargins(0, dp(8), dp(8), 0);
        root.addView(menuButton, menuParams);
        setContentView(root);
        android.content.SharedPreferences prefs = getPreferences(MODE_PRIVATE);
        Mobile.setFastBoot(prefs.getBoolean("fast_boot", false));
        Mobile.setDisplayMode(prefs.getString("display_mode", "4:3"));
        Mobile.setTextureFilter(prefs.getString("texture_filter", "nearest"));
        Mobile.setRenderer(prefs.getString("renderer", "software"));
        Mobile.setUpscale(prefs.getString("upscale", "1x"));
        restoreGameLibrary();
    }

    private void hideSystemUi() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                        | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                        | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN);
    }

    private int dp(int value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }

    private void chooseGameFolder() {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
        startActivityForResult(intent, REQUEST_GAME_FOLDER);
    }

    private void chooseBios() {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.setType("*/*");
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        startActivityForResult(intent, REQUEST_BIOS);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (resultCode != RESULT_OK || data == null || data.getData() == null) return;

        Uri uri = data.getData();
        try {
            if (requestCode == REQUEST_GAME_FOLDER) {
                getContentResolver().takePersistableUriPermission(uri,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION);
                importGameFolder(uri);
            } else if (requestCode == REQUEST_BIOS) {
                File bios = new File(getCacheDir(), "saturn-bios.bin");
                copyUri(uri, bios);
                String result = Mobile.loadBIOS(bios.getAbsolutePath());
                statusView.setText(result == null || result.isEmpty()
                        ? "BIOS loaded. " + Mobile.getStatus() : result);
            }
        } catch (Exception e) {
            statusView.setText("Unable to open selected content: " + e.getMessage());
        }
    }

    private void importGameFolder(Uri uri) throws Exception {
        DocumentFile folder = DocumentFile.fromTreeUri(this, uri);
        if (folder == null || !folder.isDirectory()) {
            statusView.setText("Please select a folder.");
            return;
        }

        String folderName = folder.getName();
        if (folderName == null || folderName.trim().isEmpty()) folderName = "games";
        gameDirectory = new File(getCacheDir(), "games/" + folderName.replaceAll("[^a-zA-Z0-9._-]", "_"));
        if (!gameDirectory.exists() && !gameDirectory.mkdirs()) {
            throw new IllegalStateException("Could not create game cache directory");
        }

        List<File> discs = new ArrayList<>();
        libraryCovers.clear();
        for (DocumentFile entry : folder.listFiles()) {
            if (!entry.isFile() || entry.getName() == null) continue;
            String name = entry.getName();
            String lower = name.toLowerCase(java.util.Locale.ROOT);
            File destination = new File(gameDirectory, name);
            copyUri(entry.getUri(), destination);
            if (lower.endsWith(".chd") || lower.endsWith(".cue")) {
                discs.add(destination);
            } else if (lower.endsWith(".png") || lower.endsWith(".jpg")
                    || lower.endsWith(".jpeg") || lower.endsWith(".webp")) {
                String stem = name.substring(0, name.lastIndexOf('.')).toLowerCase(java.util.Locale.ROOT);
                libraryCovers.put(stem, destination);
            }
        }

        if (discs.isEmpty()) {
            statusView.setText("No .CHD or .CUE files found in this folder.");
            return;
        }
        libraryDiscs.clear();
        libraryDiscs.addAll(discs);
        persistGameLibrary();
        rebuildCoverIndex();
        renderGameLibrary();
        statusView.setText(discs.size() + " Saturn game" + (discs.size() == 1 ? "" : "s")
                + " found. Looking for cover art online...");
        downloadMissingCovers();
    }

    private void persistGameLibrary() {
        List<String> paths = new ArrayList<>();
        for (File disc : libraryDiscs) paths.add(disc.getAbsolutePath());
        getPreferences(MODE_PRIVATE).edit()
                .putString("game_library_paths", android.text.TextUtils.join("\n", paths))
                .apply();
    }

    private void restoreGameLibrary() {
        String saved = getPreferences(MODE_PRIVATE).getString("game_library_paths", "");
        if (saved.isEmpty()) {
            renderGameLibrary();
            return;
        }
        libraryDiscs.clear();
        for (String path : saved.split("\n")) {
            if (path.isEmpty()) continue;
            File disc = new File(path);
            if (disc.isFile()) libraryDiscs.add(disc);
        }
        rebuildCoverIndex();
        renderGameLibrary();
        if (!libraryDiscs.isEmpty()) {
            statusView.setText(libraryDiscs.size() + " saved Saturn game"
                    + (libraryDiscs.size() == 1 ? "" : "s") + " ready.");
        }
    }

    private void rebuildCoverIndex() {
        libraryCovers.clear();
        File coverCache = new File(getCacheDir(), "covers");
        if (coverCache.isDirectory()) {
            File[] cached = coverCache.listFiles();
            if (cached != null) for (File file : cached) {
                if (file.isFile() && file.getName().toLowerCase(java.util.Locale.ROOT).endsWith(".png")) {
                    libraryCovers.put(fileStem(file.getName()), file);
                }
            }
        }
        java.util.Set<String> scanned = new java.util.HashSet<>();
        for (File disc : libraryDiscs) {
            File parent = disc.getParentFile();
            if (parent == null || !scanned.add(parent.getAbsolutePath())) continue;
            File[] files = parent.listFiles();
            if (files == null) continue;
            for (File file : files) {
                if (!file.isFile()) continue;
                String lower = file.getName().toLowerCase(java.util.Locale.ROOT);
                if (lower.endsWith(".png") || lower.endsWith(".jpg")
                        || lower.endsWith(".jpeg") || lower.endsWith(".webp")) {
                    libraryCovers.put(fileStem(file.getName()), file);
                }
            }
        }
    }

    private void downloadMissingCovers() {
        final List<File> missing = new ArrayList<>();
        for (File disc : libraryDiscs) {
            if (!libraryCovers.containsKey(fileStem(disc.getName()))) missing.add(disc);
        }
        if (missing.isEmpty()) {
            statusView.setText("Cover art is already available for this library.");
            return;
        }
        new Thread(() -> {
            int downloaded = 0;
            try {
                String html = readText(new URL(BOXART_INDEX));
                List<String> remoteNames = new ArrayList<>();
                Matcher matcher = Pattern.compile("href=\"([^\"]+\\.png)\"", Pattern.CASE_INSENSITIVE).matcher(html);
                while (matcher.find()) {
                    String href = matcher.group(1);
                    String decoded;
                    try { decoded = URLDecoder.decode(href, "UTF-8"); }
                    catch (Exception ignored) { decoded = href; }
                    if (!decoded.startsWith("../")) remoteNames.add(href);
                }
                File coverCache = new File(getCacheDir(), "covers");
                if (!coverCache.exists()) coverCache.mkdirs();
                for (File disc : missing) {
                    String key = normalizeCoverName(fileStem(disc.getName()));
                    String best = null;
                    int bestScore = Integer.MAX_VALUE;
                    for (String href : remoteNames) {
                        String remoteName;
                        try { remoteName = URLDecoder.decode(href.substring(href.lastIndexOf('/') + 1), "UTF-8"); }
                        catch (Exception ignored) { remoteName = href; }
                        String remoteKey = normalizeCoverName(fileStem(remoteName));
                        if (remoteKey.equals(key)) { best = href; bestScore = 0; break; }
                        if (remoteKey.startsWith(key) && remoteKey.length() - key.length() < bestScore) {
                            best = href;
                            bestScore = remoteKey.length() - key.length();
                        }
                    }
                    if (best == null) continue;
                    File destination = new File(coverCache, fileStem(disc.getName()) + ".png");
                    if (!destination.isFile() || destination.length() == 0) {
                        URL imageUrl = new URL(new URL(BOXART_INDEX), best);
                        HttpURLConnection connection = (HttpURLConnection) imageUrl.openConnection();
                        connection.setConnectTimeout(8000);
                        connection.setReadTimeout(12000);
                        connection.setInstanceFollowRedirects(true);
                        try (InputStream in = connection.getInputStream();
                             FileOutputStream out = new FileOutputStream(destination)) {
                            byte[] buffer = new byte[16 * 1024];
                            int count;
                            while ((count = in.read(buffer)) != -1) out.write(buffer, 0, count);
                        } finally {
                            connection.disconnect();
                        }
                    }
                    if (destination.isFile() && destination.length() > 0) {
                        synchronized (libraryCovers) { libraryCovers.put(fileStem(disc.getName()), destination); }
                        downloaded++;
                    }
                }
            } catch (Exception ignored) {
                // The library remains usable when the image source is unavailable.
            }
            final int result = downloaded;
            runOnUiThread(() -> {
                renderGameLibrary();
                statusView.setText(result > 0
                        ? "Downloaded " + result + " cover" + (result == 1 ? "" : "s") + "."
                        : "No matching online covers found. You can add local images beside the games.");
            });
        }, "Saturday-cover-downloader").start();
    }

    private String readText(URL url) throws Exception {
        HttpURLConnection connection = (HttpURLConnection) url.openConnection();
        connection.setConnectTimeout(8000);
        connection.setReadTimeout(12000);
        connection.setRequestProperty("User-Agent", "Saturday-Android/1.0");
        try (InputStream in = connection.getInputStream();
             java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream()) {
            byte[] buffer = new byte[16 * 1024];
            int count;
            while ((count = in.read(buffer)) != -1) out.write(buffer, 0, count);
            return new String(out.toByteArray(), StandardCharsets.UTF_8);
        } finally {
            connection.disconnect();
        }
    }

    private String normalizeCoverName(String name) {
        String lower = name.toLowerCase(java.util.Locale.ROOT);
        lower = lower.replaceAll("\\(disc ?[0-9]+\\)", "");
        lower = lower.replaceAll("\\(\\s*(usa|europe|european|japan|world|asia|korea|brazil|france|germany|italy|spain|sweden|australia)[^)]*\\)", "");
        return lower.replaceAll("[^a-z0-9]", "");
    }

    private void renderGameLibrary() {
        if (gameListContainer == null) return;
        gameListContainer.removeAllViews();
        String query = searchInput == null ? "" : searchInput.getText().toString().trim().toLowerCase(java.util.Locale.ROOT);
        renderRecentGames();
        int shown = 0;
        for (File disc : libraryDiscs) {
            if (!disc.getName().toLowerCase(java.util.Locale.ROOT).contains(query)) continue;
            shown++;
            LinearLayout item = new LinearLayout(this);
            item.setOrientation(LinearLayout.HORIZONTAL);
            item.setGravity(android.view.Gravity.CENTER_VERTICAL);
            item.setPadding(dp(10), dp(4), dp(6), dp(4));
            item.setBackgroundColor(shown % 2 == 0 ? 0xFF1F2937 : 0xFF273449);

            ImageView cover = createCoverView(disc, dp(54), dp(62));
            LinearLayout.LayoutParams coverParams = new LinearLayout.LayoutParams(dp(54), dp(62));
            coverParams.rightMargin = dp(10);
            item.addView(cover, coverParams);

            TextView title = new TextView(this);
            title.setText(disc.getName());
            title.setTextColor(0xFFFFFFFF);
            title.setTextSize(13);
            title.setMaxLines(2);
            title.setEllipsize(android.text.TextUtils.TruncateAt.END);
            item.addView(title, new LinearLayout.LayoutParams(0, dp(48), 1));

            Button play = new Button(this);
            play.setText("PLAY");
            play.setAllCaps(false);
            play.setTextSize(12);
            play.setOnClickListener(v -> startGame(disc));
            item.addView(play, new LinearLayout.LayoutParams(dp(82), dp(44)));
            LinearLayout.LayoutParams itemParams = new LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
            itemParams.bottomMargin = dp(3);
            gameListContainer.addView(item, itemParams);
        }
        if (shown == 0) {
            TextView empty = new TextView(this);
            empty.setText(libraryDiscs.isEmpty()
                    ? "Your library is empty. Choose a folder with .CHD or .CUE games."
                    : "No games match your search.");
            empty.setTextColor(0xFFD1D5DB);
            empty.setTextSize(13);
            empty.setPadding(dp(10), dp(12), dp(10), dp(12));
            gameListContainer.addView(empty);
        }
    }

    private ImageView createCoverView(File disc, int width, int height) {
        ImageView cover = new ImageView(this);
        cover.setScaleType(ImageView.ScaleType.CENTER_CROP);
        cover.setBackgroundColor(0xFF374151);
        File image = libraryCovers.get(fileStem(disc.getName()));
        if (image != null && image.isFile()) {
            BitmapFactory.Options options = new BitmapFactory.Options();
            options.inJustDecodeBounds = true;
            BitmapFactory.decodeFile(image.getAbsolutePath(), options);
            options.inSampleSize = 1;
            while (options.outWidth / options.inSampleSize > width * 2
                    || options.outHeight / options.inSampleSize > height * 2) {
                options.inSampleSize *= 2;
            }
            options.inJustDecodeBounds = false;
            cover.setImageBitmap(BitmapFactory.decodeFile(image.getAbsolutePath(), options));
        } else {
            cover.setImageResource(android.R.drawable.ic_menu_gallery);
            cover.setScaleType(ImageView.ScaleType.CENTER_INSIDE);
            cover.setPadding(dp(12), dp(12), dp(12), dp(12));
        }
        return cover;
    }

    private String fileStem(String name) {
        int dot = name.lastIndexOf('.');
        return (dot > 0 ? name.substring(0, dot) : name).toLowerCase(java.util.Locale.ROOT);
    }

    private List<String> loadRecentPaths() {
        String saved = getPreferences(MODE_PRIVATE).getString("recent_games_paths", "");
        List<String> paths = new ArrayList<>();
        if (saved.isEmpty()) return paths;
        for (String path : saved.split("\n")) {
            if (!path.isEmpty()) paths.add(path);
        }
        return paths;
    }

    private void renderRecentGames() {
        if (recentListContainer == null) return;
        recentListContainer.removeAllViews();
        java.util.Set<String> available = new java.util.HashSet<>();
        for (File disc : libraryDiscs) available.add(disc.getAbsolutePath());
        int shown = 0;
        for (String path : loadRecentPaths()) {
            if (!available.contains(path)) continue;
            File disc = new File(path);
            LinearLayout recentRow = new LinearLayout(this);
            recentRow.setOrientation(LinearLayout.HORIZONTAL);
            recentRow.setGravity(android.view.Gravity.CENTER_VERTICAL);
            ImageView cover = createCoverView(disc, dp(34), dp(40));
            recentRow.addView(cover, new LinearLayout.LayoutParams(dp(34), dp(40)));
            Button recentButton = new Button(this);
            recentButton.setText(disc.getName());
            recentButton.setAllCaps(false);
            recentButton.setTextSize(12);
            recentButton.setGravity(android.view.Gravity.START | android.view.Gravity.CENTER_VERTICAL);
            recentButton.setOnClickListener(v -> startGame(disc));
            recentRow.addView(recentButton, new LinearLayout.LayoutParams(0, dp(42), 1));
            recentListContainer.addView(recentRow, new LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT, dp(42)));
            if (++shown == 3) break;
        }
        if (shown == 0) {
            TextView empty = new TextView(this);
            empty.setText("Your recently played games will appear here.");
            empty.setTextColor(0xFFD1D5DB);
            empty.setTextSize(12);
            empty.setPadding(dp(8), dp(2), dp(8), dp(6));
            recentListContainer.addView(empty);
        }
    }

    private void rememberRecentGame(File disc) {
        List<String> recent = new ArrayList<>();
        String path = disc.getAbsolutePath();
        recent.add(path);
        for (String oldPath : loadRecentPaths()) {
            if (!oldPath.equals(path) && recent.size() < 10) recent.add(oldPath);
        }
        getPreferences(MODE_PRIVATE).edit()
                .putString("recent_games_paths", android.text.TextUtils.join("\n", recent))
                .apply();
    }

    private void showInGameSettings() {
        final String[] choices = {
                "Renderer: Software" + (getPreferences(MODE_PRIVATE).getString("renderer", "software").equals("software") ? " ✓" : ""),
                "Renderer: GPU" + (getPreferences(MODE_PRIVATE).getString("renderer", "software").equals("gpu") ? " ✓" : ""),
                "Internal resolution: 1x" + (getPreferences(MODE_PRIVATE).getString("upscale", "1x").equals("1x") ? " ✓" : ""),
                "Internal resolution: 2x" + (getPreferences(MODE_PRIVATE).getString("upscale", "1x").equals("2x") ? " ✓" : ""),
                "Internal resolution: 4x" + (getPreferences(MODE_PRIVATE).getString("upscale", "1x").equals("4x") ? " ✓" : ""),
                "Internal resolution: 8x" + (getPreferences(MODE_PRIVATE).getString("upscale", "1x").equals("8x") ? " ✓" : ""),
                "Internal resolution: 16x" + (getPreferences(MODE_PRIVATE).getString("upscale", "1x").equals("16x") ? " ✓" : ""),
                "Display: " + getPreferences(MODE_PRIVATE).getString("display_mode", "4:3"),
                "Texture filter: " + getPreferences(MODE_PRIVATE).getString("texture_filter", "nearest"),
                "Fast boot: " + (getPreferences(MODE_PRIVATE).getBoolean("fast_boot", false) ? "On" : "Off"),
                "Controller mapping…",
                "Save state now",
                "Load state…"
        };
        new AlertDialog.Builder(this)
                .setTitle("Saturday settings")
                .setItems(choices, (dialog, which) -> {
                    String result = "";
                    android.content.SharedPreferences prefs = getPreferences(MODE_PRIVATE);
                    if (which == 0 || which == 1) {
                        String next = which == 0 ? "software" : "gpu";
                        result = Mobile.setRenderer(next);
                        if (result == null || result.isEmpty()) prefs.edit().putString("renderer", next).apply();
                    } else if (which >= 2 && which <= 6) {
                        String[] scales = {"1x", "2x", "4x", "8x", "16x"};
                        String next = scales[which - 2];
                        result = Mobile.setUpscale(next);
                        if (result == null || result.isEmpty()) prefs.edit().putString("upscale", next).apply();
                    } else if (which == 7) {
                        String current = prefs.getString("display_mode", "4:3");
                        String next = current.equals("4:3") ? "stretch" : "4:3";
                        prefs.edit().putString("display_mode", next).apply();
                        result = Mobile.setDisplayMode(next);
                    } else if (which == 8) {
                        String current = prefs.getString("texture_filter", "nearest");
                        String next = current.equals("nearest") ? "bilinear" : "nearest";
                        prefs.edit().putString("texture_filter", next).apply();
                        result = Mobile.setTextureFilter(next);
                    } else if (which == 9) {
                        boolean enabled = !prefs.getBoolean("fast_boot", false);
                        prefs.edit().putBoolean("fast_boot", enabled).apply();
                        Mobile.setFastBoot(enabled);
                        result = "Fast boot " + (enabled ? "enabled" : "disabled")
                                + ". It applies on the next game launch; HLE BIOS already skips the retail BIOS.";
                    } else if (which == 10) {
                        showControllerMappingDialog();
                        return;
                    } else if (which == 11) {
                        File stateFile = currentStateFile();
                        result = stateFile == null ? "Start a game before saving a state."
                                : Mobile.saveState(stateFile.getAbsolutePath());
                    } else if (which == 12) {
                        File stateFile = currentStateFile();
                        if (stateFile == null) {
                            result = "Start the matching game before loading a state.";
                        } else if (!stateFile.isFile()) {
                            result = "No save state exists for this game yet.";
                        } else {
                            result = Mobile.loadState(stateFile.getAbsolutePath());
                            if (result != null && result.startsWith("Save state rejected")) {
                                gameRunning = false;
                                gameActions.setVisibility(View.GONE);
                                controlPanel.setVisibility(View.VISIBLE);
                                menuButton.setVisibility(View.GONE);
                                uiHandler.removeCallbacks(diagnosticsUpdater);
                            }
                        }
                    }
                    statusView.setText(result == null || result.isEmpty() ? Mobile.getDiagnostics() : result);
                })
                .setNegativeButton("Close", null)
                .show();
    }

    private static final int[] MAPPABLE_KEYS = {
            KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN,
            KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_DPAD_RIGHT,
            KeyEvent.KEYCODE_BUTTON_A, KeyEvent.KEYCODE_BUTTON_B,
            KeyEvent.KEYCODE_BUTTON_X, KeyEvent.KEYCODE_BUTTON_Y,
            KeyEvent.KEYCODE_BUTTON_1, KeyEvent.KEYCODE_BUTTON_2,
            KeyEvent.KEYCODE_BUTTON_L1, KeyEvent.KEYCODE_BUTTON_L2,
            KeyEvent.KEYCODE_BUTTON_R1, KeyEvent.KEYCODE_BUTTON_R2,
            KeyEvent.KEYCODE_BUTTON_START, KeyEvent.KEYCODE_ENTER
    };

    private static final String[] MAPPABLE_KEY_NAMES = {
            "D-pad Up", "D-pad Down", "D-pad Left", "D-pad Right",
            "Face button A", "Face button B", "Face button X", "Face button Y",
            "Extra button 1", "Extra button 2", "L1", "L2", "R1", "R2",
            "Start", "Enter"
    };

    private static final String[] SATURN_TARGETS = {
            "Disabled", "Up", "Down", "Left", "Right", "A", "B", "C",
            "X", "Y", "Z", "L", "R", "Start"
    };

    private void showControllerMappingDialog() {
        final android.content.SharedPreferences prefs = getPreferences(MODE_PRIVATE);
        new AlertDialog.Builder(this)
                .setTitle("Choose a physical input to remap")
                .setItems(MAPPABLE_KEY_NAMES, (dialog, index) -> {
                    int keyCode = MAPPABLE_KEYS[index];
                    int defaultTarget = defaultSaturnButtonForKey(keyCode);
                    int current = prefs.getInt("controller_map_" + keyCode, defaultTarget);
                    String[] labels = new String[SATURN_TARGETS.length];
                    for (int i = 0; i < labels.length; i++) {
                        labels[i] = SATURN_TARGETS[i] + (current == i - 1 ? " ✓" : "");
                    }
                    new AlertDialog.Builder(this)
                            .setTitle(MAPPABLE_KEY_NAMES[index] + " → Saturn button")
                            .setSingleChoiceItems(labels, current + 1, (targetDialog, targetIndex) -> {
                                int target = targetIndex - 1;
                                prefs.edit().putInt("controller_map_" + keyCode, target).apply();
                                targetDialog.dismiss();
                                statusView.setText(MAPPABLE_KEY_NAMES[index] + " mapped to "
                                        + SATURN_TARGETS[targetIndex] + ".");
                            })
                            .setNegativeButton("Cancel", null)
                            .show();
                })
                .setNegativeButton("Close", null)
                .show();
    }

    private File currentStateFile() {
        if (currentGame == null) return null;
        File directory = new File(getFilesDir(), "states");
        if (!directory.exists() && !directory.mkdirs()) return null;
        String stem = fileStem(currentGame.getName()).replaceAll("[^a-z0-9._-]", "_");
        String pathKey = Integer.toHexString(currentGame.getAbsolutePath().hashCode());
        return new File(directory, stem + "-" + pathKey + ".state");
    }

    private void showDiscSwapDialog() {
        if (libraryDiscs.isEmpty()) {
            statusView.setText("Scan a game folder before swapping discs.");
            return;
        }
        String[] titles = new String[libraryDiscs.size()];
        for (int i = 0; i < libraryDiscs.size(); i++) titles[i] = libraryDiscs.get(i).getName();
        new AlertDialog.Builder(this)
                .setTitle("Insert disc")
                .setItems(titles, (dialog, which) -> {
                    File disc = libraryDiscs.get(which);
                    String result = Mobile.swapDisc(disc.getAbsolutePath());
                    statusView.setText(result == null || result.isEmpty() ? Mobile.getDiagnostics() : result);
                })
                .setNegativeButton("Cancel", null)
                .show();
    }

    private void confirmQuitGame() {
        new AlertDialog.Builder(this)
                .setTitle("Quit game?")
                .setMessage("Close the current Saturn session?")
                .setNegativeButton("Cancel", null)
                .setPositiveButton("Quit", (dialog, which) -> {
                    Mobile.quitGame();
                    gameRunning = false;
                    uiHandler.removeCallbacks(diagnosticsUpdater);
                    gameActions.setVisibility(View.GONE);
                    controlPanel.setVisibility(View.VISIBLE);
                    menuButton.setVisibility(View.GONE);
                    statusView.setText(Mobile.getStatus());
                })
                .show();
    }

    private void startGame(File disc) {
        String result = Mobile.loadDisc(disc.getAbsolutePath());
        gameRunning = result == null || result.isEmpty();
        if (gameRunning) currentGame = disc;
        statusView.setText(gameRunning ? Mobile.getDiagnostics() : result);
        if (gameRunning) {
            gameActions.setVisibility(View.VISIBLE);
            rememberRecentGame(disc);
            renderRecentGames();
            controlPanel.setVisibility(View.GONE);
            menuButton.setVisibility(View.VISIBLE);
            menuButton.setText("MENU");
            uiHandler.removeCallbacks(diagnosticsUpdater);
        }
    }

    private void copyUri(Uri uri, File destination) throws Exception {
        try (InputStream in = getContentResolver().openInputStream(uri);
             FileOutputStream out = new FileOutputStream(destination)) {
            if (in == null) throw new IllegalStateException("Could not read selected file");
            byte[] buffer = new byte[64 * 1024];
            int count;
            while ((count = in.read(buffer)) != -1) out.write(buffer, 0, count);
        }
    }

    @Override
    public boolean dispatchKeyEvent(KeyEvent event) {
        int button = saturnButtonForKey(event.getKeyCode());
        if (button >= 0) {
            Mobile.setButton(button, event.getAction() == KeyEvent.ACTION_DOWN);
            return true;
        }
        return super.dispatchKeyEvent(event);
    }

    @Override
    public boolean onGenericMotionEvent(MotionEvent event) {
        if ((event.getSource() & android.view.InputDevice.SOURCE_JOYSTICK)
                == android.view.InputDevice.SOURCE_JOYSTICK
                && event.getAction() == MotionEvent.ACTION_MOVE) {
            float x = event.getAxisValue(MotionEvent.AXIS_X);
            float y = event.getAxisValue(MotionEvent.AXIS_Y);
            Mobile.setButton(2, x < -0.45f);
            Mobile.setButton(3, x > 0.45f);
            Mobile.setButton(0, y < -0.45f);
            Mobile.setButton(1, y > 0.45f);
            return true;
        }
        return super.onGenericMotionEvent(event);
    }

    private int saturnButtonForKey(int keyCode) {
        int defaultTarget = defaultSaturnButtonForKey(keyCode);
        if (defaultTarget < 0) return -1;
        return getPreferences(MODE_PRIVATE).getInt("controller_map_" + keyCode, defaultTarget);
    }

    private int defaultSaturnButtonForKey(int keyCode) {
        switch (keyCode) {
            case KeyEvent.KEYCODE_DPAD_UP: return 0;
            case KeyEvent.KEYCODE_DPAD_DOWN: return 1;
            case KeyEvent.KEYCODE_DPAD_LEFT: return 2;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return 3;
            // Standard Android gamepad face buttons.
            case KeyEvent.KEYCODE_BUTTON_A: return 4; // Saturn A
            case KeyEvent.KEYCODE_BUTTON_B: return 5; // Saturn B
            case KeyEvent.KEYCODE_BUTTON_X: return 7; // Saturn X
            case KeyEvent.KEYCODE_BUTTON_Y: return 8; // Saturn Y
            // Extra numbered buttons cover six-button controllers that
            // expose their additional face buttons through Android's generic IDs.
            case KeyEvent.KEYCODE_BUTTON_1: return 6; // Saturn C
            case KeyEvent.KEYCODE_BUTTON_2: return 9; // Saturn Z
            case KeyEvent.KEYCODE_BUTTON_L1:
            case KeyEvent.KEYCODE_BUTTON_L2: return 10; // Saturn L
            case KeyEvent.KEYCODE_BUTTON_R1:
            case KeyEvent.KEYCODE_BUTTON_R2: return 11; // Saturn R
            case KeyEvent.KEYCODE_BUTTON_START:
            case KeyEvent.KEYCODE_ENTER: return 12;
            default: return -1;
        }
    }

    @Override
    protected void onPause() {
        uiHandler.removeCallbacks(diagnosticsUpdater);
        Mobile.saveBackupRAM();
        if (ebitenView != null) ebitenView.suspendGame();
        super.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();
        hideSystemUi();
        if (ebitenView != null) ebitenView.resumeGame();
        if (gameRunning && controlPanel != null && controlPanel.getVisibility() == View.VISIBLE)
            uiHandler.postDelayed(diagnosticsUpdater, 1000);
    }
}
