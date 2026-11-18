package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

// AppImage desktop integration: "Install" copies the running AppImage to
// ~/Applications and adds a menu entry with an icon; "Uninstall" removes them.

const appImageName = "Egida.AppImage"

func appImagePath() string { return os.Getenv("APPIMAGE") }

func homePath(p ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, p...)...)
}

func desktopFile() string { return homePath(".local", "share", "applications", "egida.desktop") }
func iconFile() string {
	return homePath(".local", "share", "icons", "hicolor", "256x256", "apps", "egida.png")
}
func installedAppImage() string { return homePath("Applications", appImageName) }

func appImageInstalled() bool {
	_, err := os.Stat(desktopFile())
	return err == nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func installAppImage() error {
	src := appImagePath()
	dst := installedAppImage()
	if s, _ := filepath.EvalSymlinks(src); s != dst {
		if err := copyFile(src, dst, 0o755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(iconFile()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(iconFile(), iconPNG, 0o644); err != nil {
		return err
	}
	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Name[en]=Egida
Comment=%s
Comment[en]=Departmental task and workflow management
Exec="%s" %%U
Icon=egida
Terminal=false
Categories=Office;ProjectManagement;
StartupWMClass=egida
`, appName, "Управление задачами и этапами работы отделов", dst)
	if err := os.MkdirAll(filepath.Dir(desktopFile()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(desktopFile(), []byte(desktop), 0o644)
}

func uninstallAppImage() error {
	for _, p := range []string{desktopFile(), iconFile(), installedAppImage()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// appImageMenuItems returns the install/uninstall items when running as an AppImage.
func (u *ui) appImageMenuItems() []*fyne.MenuItem {
	if appImagePath() == "" {
		return nil
	}
	if appImageInstalled() {
		return []*fyne.MenuItem{fyne.NewMenuItem(T("Удалить из системы…"), func() {
			dialog.ShowConfirm(T("Удалить из системы"), T("Удалить ярлык «Эгиды» из меню приложений и копию программы из ~/Applications? Данные не удаляются."),
				func(ok bool) {
					if !ok {
						return
					}
					if err := uninstallAppImage(); err != nil {
						dialog.ShowError(err, u.win)
						return
					}
					u.buildMenu()
					dialog.ShowInformation(appName, T("Программа удалена из меню приложений."), u.win)
				}, u.win)
		})}
	}
	return []*fyne.MenuItem{fyne.NewMenuItem(T("Установить в систему"), func() {
		if err := installAppImage(); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.buildMenu()
		dialog.ShowInformation(appName, Tf("Программа установлена: %s. Ярлык добавлен в меню приложений.", installedAppImage()), u.win)
	})}
}
