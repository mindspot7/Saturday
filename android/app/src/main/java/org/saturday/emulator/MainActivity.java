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
import android.widget.TextView;

import androidx.documentfile.provider.DocumentFile;

import org.saturday.emulator.mobile.EbitenView;
import org.saturday.emulator.mobile.Mobile;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.List;

public final class MainActivity extends Activity {
    private static final int REQUEST_GAME_FOLDER = 101;
    private static final int REQUEST_BIOS = 102;

    private EbitenView ebitenView;
    private TextView statusView;
    private LinearLayout controlPanel;
    private Button menuButton;
    private LinearLayout gameListContainer;
    private LinearLayout recentListContainer;
    private EditText searchInput;
    private final List<File> libraryDiscs = new ArrayList<>();
    private File gameDirectory;
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
        for (DocumentFile entry : folder.listFiles()) {
            if (!entry.isFile() || entry.getName() == null) continue;
            String name = entry.getName();
            String lower = name.toLowerCase(java.util.Locale.ROOT);
            File destination = new File(gameDirectory, name);
            copyUri(entry.getUri(), destination);
            if (lower.endsWith(".chd") || lower.endsWith(".cue")) discs.add(destination);
        }

        if (discs.isEmpty()) {
            statusView.setText("No .CHD or .CUE files found in this folder.");
            return;
        }
        libraryDiscs.clear();
        libraryDiscs.addAll(discs);
        persistGameLibrary();
        renderGameLibrary();
        statusView.setText(discs.size() + " Saturn game" + (discs.size() == 1 ? "" : "s")
                + " found. Select a title to start playing.");
    }

    private void persistGameLibrary() {
        List<String> paths = new ArrayList<>();
        for (File disc : libraryDiscs) paths.add(disc.getAbsolutePath());
        getPreferences(MODE_PRIVATE).edit()
                .putString("game_library_paths", android.text.TextUtils.join("\\n", paths))
                .apply();
    }

    private void restoreGameLibrary() {
        String saved = getPreferences(MODE_PRIVATE).getString("game_library_paths", "");
        if (saved.isEmpty()) {
            renderGameLibrary();
            return;
        }
        libraryDiscs.clear();
        for (String path : saved.split("\\n")) {
            if (path.isEmpty()) continue;
            File disc = new File(path);
            if (disc.isFile()) libraryDiscs.add(disc);
        }
        renderGameLibrary();
        if (!libraryDiscs.isEmpty()) {
            statusView.setText(libraryDiscs.size() + " saved Saturn game"
                    + (libraryDiscs.size() == 1 ? "" : "s") + " ready.");
        }
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

    private List<String> loadRecentPaths() {
        String saved = getPreferences(MODE_PRIVATE).getString("recent_games_paths", "");
        List<String> paths = new ArrayList<>();
        if (saved.isEmpty()) return paths;
        for (String path : saved.split("\\n")) {
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
            Button recentButton = new Button(this);
            recentButton.setText("▶  " + disc.getName());
            recentButton.setAllCaps(false);
            recentButton.setTextSize(12);
            recentButton.setGravity(android.view.Gravity.START | android.view.Gravity.CENTER_VERTICAL);
            recentButton.setOnClickListener(v -> startGame(disc));
            recentListContainer.addView(recentButton, new LinearLayout.LayoutParams(
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
                .putString("recent_games_paths", android.text.TextUtils.join("\\n", recent))
                .apply();
    }

    private void startGame(File disc) {
        String result = Mobile.loadDisc(disc.getAbsolutePath());
        gameRunning = result == null || result.isEmpty();
        statusView.setText(gameRunning ? Mobile.getDiagnostics() : result);
        if (gameRunning) {
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
